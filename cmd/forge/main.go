// forge is the command-line companion of the goforge framework.
//
// Subcommands group lifecycle operations a goforge codebase normally
// needs: scaffolding new resources, running migrations, generating
// the OpenAPI spec, exercising the load-test harness, listing
// installed modules and diagnosing the runtime configuration.
//
// The CLI is intentionally dependency-light - it uses only the Go
// standard library so `go install github.com/dedeez14/goforge/cmd/forge`
// produces a single static binary that can run before the main API.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"
)

// version is overridden via -ldflags='-X main.version=…' at build time.
var version = "dev"

type command struct {
	name string
	desc string
	run  func(ctx context.Context, args []string) error
}

var commands = []command{
	{name: "doctor", desc: "Diagnose the local environment (Go, DB, env, ports)", run: cmdDoctor},
	{name: "scaffold", desc: "Generate a new resource (domain + usecase + repo + http + migration)", run: cmdScaffold},
	{name: "gen", desc: "Template-driven generators (`forge gen resource <Name>`)", run: cmdGen},
	{name: "migrate", desc: "Run database migrations (up | down | status)", run: cmdMigrate},
	{name: "openapi", desc: "Print the OpenAPI 3.1 spec by hitting the running API", run: cmdOpenAPI},
	{name: "bench", desc: "Forward to cmd/bench load-test harness", run: cmdBench},
	{name: "module", desc: "Inspect modules registered with the running API", run: cmdModule},
	{name: "rbac", desc: "Permission registry: `forge rbac sync` upserts code references into the DB", run: cmdRBACSync},
	{name: "version", desc: "Print version", run: func(_ context.Context, _ []string) error {
		fmt.Printf("forge %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
		return nil
	}},
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name := os.Args[1]
	if name == "-h" || name == "--help" || name == "help" {
		usage()
		return
	}
	for _, c := range commands {
		if c.name == name {
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			err := c.run(ctx, os.Args[2:])
			cancel()
			if err != nil {
				fmt.Fprintln(os.Stderr, "forge: "+err.Error())
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "forge: unknown command %q\n\n", name)
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Println("forge - the goforge command-line companion")
	fmt.Println("")
	fmt.Println("usage: forge <command> [options]")
	fmt.Println("")
	fmt.Println("commands:")
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, c := range commands {
		_, _ = fmt.Fprintf(w, "  %s\t%s\n", c.name, c.desc)
	}
	_ = w.Flush()
}

// cmdDoctor checks Go version, DB DSN reachability, env vars, and key
// configuration knobs that often misfire on first run.
func cmdDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	dsn := fs.String("dsn", os.Getenv("GOFORGE_DATABASE_DSN"), "Postgres DSN to ping")
	port := fs.Int("port", 8080, "API port to verify is reachable")
	apiURL := fs.String("url", "", "API base URL to verify is responding (e.g. http://localhost:8080)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	checks := []checkResult{checkGo()}

	if *dsn == "" {
		checks = append(checks, checkResult{name: "database dsn", ok: false, detail: "GOFORGE_DATABASE_DSN not set"})
	} else {
		checks = append(checks, checkDSN(ctx, *dsn))
	}

	checks = append(checks, checkPort(*port))
	if *apiURL != "" {
		checks = append(checks, checkAPI(ctx, *apiURL))
	}
	checks = append(checks, checkSecretStrength("GOFORGE_JWT_SECRET", 32))

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	failures := 0
	for _, c := range checks {
		mark := "OK"
		if !c.ok {
			mark = "FAIL"
			failures++
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", mark, c.name, c.detail)
	}
	_ = w.Flush()
	if failures > 0 {
		return fmt.Errorf("%d check(s) failed", failures)
	}
	return nil
}

type checkResult struct {
	name   string
	ok     bool
	detail string
}

func checkGo() checkResult {
	return checkResult{name: "go runtime", ok: true, detail: runtime.Version()}
}

func checkDSN(ctx context.Context, dsn string) checkResult {
	// Conservative timeout so doctor doesn't hang on a misconfigured host.
	parent, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	host := extractHost(dsn)
	if host == "" {
		return checkResult{name: "database dsn", ok: false, detail: "could not parse host from DSN"}
	}
	d := net.Dialer{}
	conn, err := d.DialContext(parent, "tcp", host)
	if err != nil {
		return checkResult{name: "database dsn", ok: false, detail: err.Error()}
	}
	_ = conn.Close()
	return checkResult{name: "database dsn", ok: true, detail: host + " reachable"}
}

func checkPort(port int) checkResult {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return checkResult{name: "api port", ok: false, detail: addr + " in use - is the API already running?"}
	}
	_ = l.Close()
	return checkResult{name: "api port", ok: true, detail: addr + " free"}
}

func checkAPI(ctx context.Context, base string) checkResult {
	parent, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(parent, http.MethodGet, strings.TrimRight(base, "/")+"/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return checkResult{name: "api healthz", ok: false, detail: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return checkResult{name: "api healthz", ok: false, detail: fmt.Sprintf("status %d", resp.StatusCode)}
	}
	return checkResult{name: "api healthz", ok: true, detail: "200 OK"}
}

func checkSecretStrength(env string, min int) checkResult {
	v := os.Getenv(env)
	if v == "" {
		return checkResult{name: env, ok: false, detail: "missing"}
	}
	if len(v) < min {
		return checkResult{name: env, ok: false, detail: fmt.Sprintf("only %d chars (need >= %d)", len(v), min)}
	}
	return checkResult{name: env, ok: true, detail: fmt.Sprintf("%d chars", len(v))}
}

func extractHost(dsn string) string {
	// Two common shapes: postgres://user:pass@host:port/db?... and key=value strings.
	if i := strings.Index(dsn, "://"); i >= 0 {
		rest := dsn[i+3:]
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		if slash := strings.IndexAny(rest, "/?"); slash >= 0 {
			rest = rest[:slash]
		}
		if !strings.Contains(rest, ":") {
			rest += ":5432"
		}
		return rest
	}
	host, port := "", "5432"
	for _, kv := range strings.Fields(dsn) {
		switch {
		case strings.HasPrefix(kv, "host="):
			host = strings.TrimPrefix(kv, "host=")
		case strings.HasPrefix(kv, "port="):
			port = strings.TrimPrefix(kv, "port=")
		}
	}
	if host == "" {
		return ""
	}
	return host + ":" + port
}

// cmdScaffold delegates to the existing shell scaffolding helper so
// the CLI stays in sync with the Make target.
func cmdScaffold(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scaffold", flag.ContinueOnError)
	name := fs.String("name", "", "resource name (PascalCase, e.g. Order)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *name == "" {
		return fmt.Errorf("--name is required")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, filepath.Join(root, "scripts", "scaffold.sh"), *name)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cmdMigrate delegates to the migrate binary if available, so we
// inherit golang-migrate's exit codes and messages.
func cmdMigrate(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: forge migrate <up|down|status|version>")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	dsn := os.Getenv("GOFORGE_DATABASE_DSN")
	if dsn == "" {
		return fmt.Errorf("GOFORGE_DATABASE_DSN must be set")
	}
	migrationsDir := filepath.Join(root, "migrations")

	bin, err := exec.LookPath("migrate")
	if err != nil {
		return fmt.Errorf("`migrate` CLI not found on PATH; install golang-migrate first: %w", err)
	}

	subcommand := args[0]
	common := []string{"-path", migrationsDir, "-database", dsn}
	full := make([]string, 0, len(common)+1)
	full = append(full, common...)
	switch subcommand {
	case "up", "down":
		full = append(full, subcommand)
	case "status", "version":
		full = append(full, "version")
	default:
		return fmt.Errorf("unsupported migrate subcommand %q", subcommand)
	}
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cmdOpenAPI fetches the running API's spec and prints it to stdout.
// Combined with `jq` or `yq` this is enough to feed any code generator.
func cmdOpenAPI(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("openapi", flag.ContinueOnError)
	url := fs.String("url", "http://localhost:8080/openapi.json", "OpenAPI document URL on the running API")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, *url)
	}
	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}

// cmdBench delegates to the benchmark harness.
func cmdBench(ctx context.Context, args []string) error {
	bin, err := exec.LookPath("bench")
	if err != nil {
		// Fall back to running via `go run` when the binary isn't installed.
		root, err := repoRoot()
		if err != nil {
			return err
		}
		fullArgs := append([]string{"run", "./cmd/bench"}, args...)
		cmd := exec.CommandContext(ctx, "go", fullArgs...)
		cmd.Dir = root
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// cmdModule lists modules registered with the running API by hitting
// the /admin/modules endpoint that the module module exposes.
func cmdModule(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("module", flag.ContinueOnError)
	url := fs.String("url", "http://localhost:8080/admin/modules", "Module registry endpoint")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 || fs.Arg(0) == "list" {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, *url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, err = io.Copy(os.Stdout, resp.Body)
		return err
	}
	return fmt.Errorf("usage: forge module list")
}

// repoRoot walks upward from the current working directory looking for
// a go.mod file. This lets `forge` work whether invoked from the repo
// root or any subdirectory.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("not inside a go module (no go.mod found)")
}

// orderedCommandNames returns the command names in declaration order;
// referenced from documentation generation tools.
func orderedCommandNames() []string {
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.name
	}
	sort.Strings(names)
	return names
}

var _ = orderedCommandNames
