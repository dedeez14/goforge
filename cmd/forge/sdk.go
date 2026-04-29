package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// cmdSDK is the parent dispatcher for every auto-generated client
// SDK. Sub-languages (ts, go, python, …) live as additional
// subcommands so the wiring stays uniform: each one downloads the
// spec, pipes it through a language-specific generator and produces
// a build under sdk/<lang>/.
//
// For now only `ts` is wired.
func cmdSDK(ctx context.Context, args []string) error {
	if len(args) < 1 || args[0] == "-h" || args[0] == "--help" {
		fmt.Println("usage: forge sdk <language> [options]")
		fmt.Println("")
		fmt.Println("languages:")
		fmt.Println("  ts    Generate the TypeScript client from /openapi.json")
		if len(args) == 0 {
			return fmt.Errorf("language is required")
		}
		return nil
	}
	switch args[0] {
	case "ts":
		return cmdSDKTS(ctx, args[1:])
	default:
		return fmt.Errorf("unknown sdk language %q (known: ts)", args[0])
	}
}

// cmdSDKTS downloads /openapi.json from a running API, drops it
// into sdk/typescript/, and runs the codegen + build pipeline.
// Requires Node.js and npm on PATH.
func cmdSDKTS(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sdk ts", flag.ContinueOnError)
	url := fs.String("url", "http://localhost:8080/openapi.json", "OpenAPI document URL on the running API")
	skipBuild := fs.Bool("skip-build", false, "stop after generate, do not run `npm run build`")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := repoRoot()
	if err != nil {
		return err
	}
	sdkDir := filepath.Join(root, "sdk", "typescript")
	if _, err := os.Stat(sdkDir); err != nil {
		return fmt.Errorf("sdk/typescript: %w", err)
	}

	// 1. Download the spec.
	specPath := filepath.Join(sdkDir, "openapi.json")
	if err := downloadSpec(ctx, *url, specPath); err != nil {
		return fmt.Errorf("fetch spec: %w", err)
	}
	fmt.Printf("wrote %s\n", specPath)

	// 2. Ensure deps. `npm ci` would fail without a lockfile, so
	// fall back to `npm install` until a lockfile is committed.
	install := "ci"
	if _, err := os.Stat(filepath.Join(sdkDir, "package-lock.json")); err != nil {
		install = "install"
	}
	if err := runIn(ctx, sdkDir, "npm", install); err != nil {
		return fmt.Errorf("npm %s: %w", install, err)
	}

	// 3. Generate.
	if err := runIn(ctx, sdkDir, "npm", "run", "generate"); err != nil {
		return fmt.Errorf("npm run generate: %w", err)
	}

	// 4. Build (unless caller only wants the sources, e.g. for
	// editing).
	if !*skipBuild {
		if err := runIn(ctx, sdkDir, "npm", "run", "build"); err != nil {
			return fmt.Errorf("npm run build: %w", err)
		}
	}

	fmt.Printf("\nTypeScript SDK ready in %s\n", sdkDir)
	return nil
}

// downloadSpec fetches the OpenAPI document at url and writes it to
// dest atomically (temp file + rename) so a half-failed download
// does not leave a broken openapi.json behind.
func downloadSpec(ctx context.Context, url, dest string) error {
	parent, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(parent, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".openapi-*.json")
	if err != nil {
		return err
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		cleanup()
		return err
	}
	return nil
}

// runIn executes command in dir with stdout/stderr piped through so
// the user sees live npm output.
func runIn(ctx context.Context, dir, command string, args ...string) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
