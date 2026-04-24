// Command bench exercises the API with a configurable number of
// concurrent workers issuing requests with unique per-request payloads.
//
// It reports throughput, p50/p95/p99 latency, and a per-status-code
// breakdown. Four scenarios are supported:
//
//	healthz  — GET /healthz (no DB, no auth)
//	register — POST /api/v1/auth/register with a unique email/name per request
//	login    — POST /api/v1/auth/login for the users produced by `register`
//	me       — GET /api/v1/auth/me with the access token produced by `register`
//
// Usage:
//
//	bench -scenario=healthz -total=500000 -concurrency=256 -url=http://localhost:8080
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type tokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type envelope[T any] struct {
	Success bool `json:"success"`
	Data    T    `json:"data"`
	Error   *struct {
		Code, Message string
	} `json:"error,omitempty"`
}

type authData struct {
	User   map[string]any `json:"user"`
	Tokens tokenPair      `json:"tokens"`
}

type userAcct struct {
	Email    string
	Password string
	Token    string
}

type result struct {
	LatencyNS int64
	Status    int
	Err       bool
}

func main() {
	var (
		scenario    = flag.String("scenario", "healthz", "healthz|register|login|me")
		total       = flag.Int("total", 500_000, "total requests")
		concurrency = flag.Int("concurrency", 256, "concurrent workers")
		baseURL     = flag.String("url", "http://localhost:8080", "API base URL")
		fixtureFile = flag.String("fixtures", "", "path to save/load accounts (json)")
		usersPrefix = flag.String("prefix", "bench", "unique prefix for generated emails")
		timeout     = flag.Duration("timeout", 30*time.Second, "per-request timeout")
	)
	flag.Parse()

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			MaxIdleConns:        *concurrency * 2,
			MaxIdleConnsPerHost: *concurrency * 2,
			MaxConnsPerHost:     *concurrency * 2,
			IdleConnTimeout:     90 * time.Second,
			DisableCompression:  true,
		},
	}

	switch *scenario {
	case "healthz":
		runHealthz(client, *baseURL, *total, *concurrency)
	case "register":
		accounts := runRegister(client, *baseURL, *total, *concurrency, *usersPrefix)
		if *fixtureFile != "" {
			saveFixtures(*fixtureFile, accounts)
		}
	case "login":
		accounts := loadFixtures(*fixtureFile)
		if len(accounts) < *total {
			log.Fatalf("need %d fixtures, have %d — run scenario=register first", *total, len(accounts))
		}
		runLogin(client, *baseURL, accounts[:*total], *concurrency)
	case "login-refresh":
		accounts := loadFixtures(*fixtureFile)
		if len(accounts) < *total {
			log.Fatalf("need %d fixtures, have %d", *total, len(accounts))
		}
		runLoginCaptureTokens(client, *baseURL, accounts[:*total], *concurrency)
		saveFixtures(*fixtureFile, accounts[:*total])
	case "me":
		accounts := loadFixtures(*fixtureFile)
		if len(accounts) < *total {
			log.Fatalf("need %d fixtures, have %d", *total, len(accounts))
		}
		runMe(client, *baseURL, accounts[:*total], *concurrency)
	default:
		log.Fatalf("unknown scenario: %s", *scenario)
	}
}

func runHealthz(c *http.Client, base string, total, conc int) {
	drive(total, conc, func(_ int) result {
		return doReq(c, http.MethodGet, base+"/healthz", nil, "")
	})
}

func runRegister(c *http.Client, base string, total, conc int, prefix string) []userAcct {
	accounts := make([]userAcct, total)
	var ok int64

	drive(total, conc, func(i int) result {
		email := fmt.Sprintf("%s-%010d@bench.test", prefix, i)
		password := "supersecretpassword"
		body, _ := json.Marshal(map[string]string{
			"email": email, "password": password, "name": fmt.Sprintf("User %d", i),
		})
		t0 := time.Now()
		resp, err := c.Post(base+"/api/v1/auth/register", "application/json", bytes.NewReader(body))
		if err != nil {
			return result{LatencyNS: time.Since(t0).Nanoseconds(), Err: true}
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusCreated {
			var env envelope[authData]
			if err := json.NewDecoder(resp.Body).Decode(&env); err == nil && env.Success {
				accounts[i] = userAcct{Email: email, Password: password, Token: env.Data.Tokens.AccessToken}
				atomic.AddInt64(&ok, 1)
			}
		} else {
			io.Copy(io.Discard, resp.Body)
		}
		return result{LatencyNS: time.Since(t0).Nanoseconds(), Status: resp.StatusCode}
	})

	fmt.Printf("  successfully-registered: %d/%d\n", ok, total)
	// Keep only populated entries.
	out := accounts[:0]
	for _, a := range accounts {
		if a.Email != "" {
			out = append(out, a)
		}
	}
	return out
}

func runLogin(c *http.Client, base string, accounts []userAcct, conc int) {
	drive(len(accounts), conc, func(i int) result {
		a := accounts[i]
		body, _ := json.Marshal(map[string]string{"email": a.Email, "password": a.Password})
		return doReq(c, http.MethodPost, base+"/api/v1/auth/login", body, "")
	})
}

func runLoginCaptureTokens(c *http.Client, base string, accounts []userAcct, conc int) {
	drive(len(accounts), conc, func(i int) result {
		a := accounts[i]
		body, _ := json.Marshal(map[string]string{"email": a.Email, "password": a.Password})
		t0 := time.Now()
		resp, err := c.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
		if err != nil {
			return result{LatencyNS: time.Since(t0).Nanoseconds(), Err: true}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var env envelope[authData]
			if err := json.NewDecoder(resp.Body).Decode(&env); err == nil && env.Success {
				accounts[i].Token = env.Data.Tokens.AccessToken
			}
		} else {
			io.Copy(io.Discard, resp.Body)
		}
		return result{LatencyNS: time.Since(t0).Nanoseconds(), Status: resp.StatusCode}
	})
}

func runMe(c *http.Client, base string, accounts []userAcct, conc int) {
	drive(len(accounts), conc, func(i int) result {
		return doReq(c, http.MethodGet, base+"/api/v1/auth/me", nil, accounts[i].Token)
	})
}

func doReq(c *http.Client, method, url string, body []byte, bearer string) result {
	var br io.Reader
	if body != nil {
		br = bytes.NewReader(body)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, url, br)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	t0 := time.Now()
	resp, err := c.Do(req)
	if err != nil {
		return result{LatencyNS: time.Since(t0).Nanoseconds(), Err: true}
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return result{LatencyNS: time.Since(t0).Nanoseconds(), Status: resp.StatusCode}
}

// drive runs fn(i) for i in [0, total), with conc workers concurrently,
// and then prints the aggregated report.
func drive(total, conc int, fn func(int) result) {
	results := make([]result, total)
	jobs := make(chan int, conc*2)
	var wg sync.WaitGroup

	start := time.Now()
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = fn(i)
			}
		}()
	}
	for i := 0; i < total; i++ {
		jobs <- i
		if i%10_000 == 0 && i > 0 {
			elapsed := time.Since(start)
			rps := float64(i) / elapsed.Seconds()
			fmt.Printf("  progress: %d/%d  elapsed=%.1fs  rps=%.0f\n", i, total, elapsed.Seconds(), rps)
		}
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)

	report(results, elapsed, conc)
}

func report(results []result, elapsed time.Duration, conc int) {
	n := len(results)
	latencies := make([]int64, 0, n)
	statuses := map[int]int{}
	errs := 0
	for _, r := range results {
		if r.Err {
			errs++
			continue
		}
		latencies = append(latencies, r.LatencyNS)
		statuses[r.Status]++
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	p := func(q float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		idx := int(float64(len(latencies)-1) * q)
		return time.Duration(latencies[idx])
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	fmt.Println("\n========== benchmark report ==========")
	fmt.Printf("  requests        : %d\n", n)
	fmt.Printf("  concurrency     : %d\n", conc)
	fmt.Printf("  duration        : %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("  throughput      : %.0f req/s\n", float64(n)/elapsed.Seconds())
	fmt.Printf("  transport errors: %d\n", errs)
	fmt.Printf("  status breakdown: %v\n", statuses)
	fmt.Printf("  latency p50     : %s\n", p(0.50).Round(time.Microsecond))
	fmt.Printf("  latency p90     : %s\n", p(0.90).Round(time.Microsecond))
	fmt.Printf("  latency p95     : %s\n", p(0.95).Round(time.Microsecond))
	fmt.Printf("  latency p99     : %s\n", p(0.99).Round(time.Microsecond))
	fmt.Printf("  latency max     : %s\n", p(1.0).Round(time.Microsecond))
	fmt.Printf("  client heapAlloc: %s\n", humanBytes(mem.HeapAlloc))
	fmt.Println("======================================")
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func saveFixtures(path string, accounts []userAcct) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("save fixtures: %v", err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(accounts); err != nil {
		log.Fatalf("encode fixtures: %v", err)
	}
	fmt.Printf("  saved %d fixtures to %s\n", len(accounts), path)
}

func loadFixtures(path string) []userAcct {
	if path == "" {
		log.Fatal("--fixtures required for login/me scenarios")
	}
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()
	var accounts []userAcct
	if err := json.NewDecoder(f).Decode(&accounts); err != nil {
		log.Fatalf("decode fixtures: %v", err)
	}
	return accounts
}
