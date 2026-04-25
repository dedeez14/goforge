package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dedeez14/goforge/internal/forge/gen"
)

// cmdGen implements `forge gen <subcommand>`. Today's only subcommand
// is `resource`, which emits the full set of files (domain, usecase,
// repo, dto, handler, migration, test) for a new aggregate.
func cmdGen(_ context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: forge gen <resource> [options]")
	}
	switch args[0] {
	case "resource":
		return runGenResource(args[1:])
	default:
		return fmt.Errorf("forge gen: unknown subcommand %q", args[0])
	}
}

func runGenResource(args []string) error {
	fs := flag.NewFlagSet("gen resource", flag.ContinueOnError)
	name := fs.String("name", "", "resource name in PascalCase, e.g. Order")
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
	mod, err := readModulePath(root + "/go.mod")
	if err != nil {
		return err
	}
	files, err := gen.Generate(root, *name, mod)
	if err != nil {
		return err
	}
	fmt.Println("created:")
	for _, f := range files {
		fmt.Printf("  %s\n", f)
	}
	fmt.Println("")
	fmt.Println("next steps:")
	fmt.Printf("  1. wire %s repo + usecase + handler in internal/app/app.go\n", *name)
	fmt.Printf("  2. add the routes under api.Group(\"/%ss\") in internal/infrastructure/server/router.go\n", strings.ToLower(*name))
	fmt.Println("  3. forge migrate up")
	fmt.Println("  4. go test -race ./...")
	return nil
}

func readModulePath(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("module path not found in %s", path)
}
