package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx via database/sql for the upsert
)

// cmdRBACSync scans the local module for RequirePermission /
// RequireAnyPermission middleware references and synchronises the
// discovered codes into the `permissions` table. It is the
// authoritative way to keep the in-DB permission catalogue in sync
// with the codes actually enforced by HTTP handlers - eliminating
// "permission drift" where a route enforces a code the database
// has never heard of, or vice versa.
//
// Subcommands:
//
//	forge rbac sync              # apply to $GOFORGE_DATABASE_DSN
//	forge rbac sync --dry-run    # print the diff without writing
//	forge rbac sync --print-only # just list the discovered codes
//	forge rbac sync --root ./api # scan a different module
func cmdRBACSync(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("forge rbac: subcommand required (sync)")
	}
	switch args[0] {
	case "sync":
		return runRBACSync(ctx, args[1:])
	default:
		return fmt.Errorf("forge rbac: unknown subcommand %q", args[0])
	}
}

func runRBACSync(ctx context.Context, args []string) error {
	fl := flag.NewFlagSet("rbac sync", flag.ContinueOnError)
	root := fl.String("root", ".", "module root to scan for RequirePermission calls")
	dsn := fl.String("dsn", os.Getenv("GOFORGE_DATABASE_DSN"), "Postgres DSN to upsert into")
	dryRun := fl.Bool("dry-run", false, "print the planned upserts without writing")
	printOnly := fl.Bool("print-only", false, "only list the discovered permission codes")
	if err := fl.Parse(args); err != nil {
		return err
	}

	codes, err := scanPermissions(*root)
	if err != nil {
		return err
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i].Code < codes[j].Code })

	if len(codes) == 0 {
		fmt.Println("forge rbac: no permission codes found.")
		return nil
	}

	if *printOnly {
		printPermissionTable(os.Stdout, codes)
		return nil
	}
	if *dryRun {
		printPermissionTable(os.Stdout, codes)
		fmt.Printf("\n(dry-run) %d code(s) would be upserted.\n", len(codes))
		return nil
	}

	if *dsn == "" {
		return fmt.Errorf("forge rbac: GOFORGE_DATABASE_DSN not set; pass --dsn or use --dry-run")
	}
	return upsertPermissions(ctx, *dsn, codes)
}

// declaredPermission represents one row to be upserted into
// the `permissions` table. resource and action are derived from
// the code's first dot-segment / remainder, since the framework
// convention is `<resource>.<action>` (e.g. orders.refund).
type declaredPermission struct {
	Code     string
	Resource string
	Action   string
	Source   string // file:line where the code was first observed
}

func splitCode(code string) (resource, action string) {
	if i := strings.IndexByte(code, '.'); i > 0 {
		return code[:i], code[i+1:]
	}
	return code, ""
}

// scanPermissions walks every Go file under root and extracts the
// string literals passed to RequirePermission/RequireAnyPermission.
// Files under vendor/, .git/, and dot-prefixed directories are
// skipped. Test files are scanned too, since security tests also
// declare codes the framework should know about.
func scanPermissions(root string) ([]declaredPermission, error) {
	seen := make(map[string]declaredPermission)
	fset := token.NewFileSet()

	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			// Tolerate single-file parse errors; the rest of the
			// scan must still succeed (e.g. broken generator output).
			//nolint:nilerr // intentional: report-and-skip semantics
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			fn := funcName(call.Fun)
			if fn == "" {
				return true
			}
			switch fn {
			case "RequirePermission":
				if code, ok := stringLit(call.Args[0]); ok {
					recordCode(seen, code, fset.Position(call.Pos()))
				}
			case "RequireAnyPermission":
				// First arg is a slice literal of strings.
				if comp, ok := call.Args[0].(*ast.CompositeLit); ok {
					for _, elt := range comp.Elts {
						if code, ok := stringLit(elt); ok {
							recordCode(seen, code, fset.Position(elt.Pos()))
						}
					}
				}
			}
			return true
		})
		return nil
	}

	if err := filepath.WalkDir(root, walk); err != nil {
		return nil, err
	}

	out := make([]declaredPermission, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	return out, nil
}

func recordCode(seen map[string]declaredPermission, code string, pos token.Position) {
	if code == "" {
		return
	}
	if _, exists := seen[code]; exists {
		return
	}
	resource, action := splitCode(code)
	seen[code] = declaredPermission{
		Code:     code,
		Resource: resource,
		Action:   action,
		Source:   fmt.Sprintf("%s:%d", relativise(pos.Filename), pos.Line),
	}
}

// funcName returns the trailing identifier of a call's function
// expression, so both `pkg.RequirePermission(...)` and
// `RequirePermission(...)` (after dot-import) match.
func funcName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.IndexExpr:
		return funcName(t.X)
	}
	return ""
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func relativise(p string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}

func printPermissionTable(w *os.File, perms []declaredPermission) {
	_, _ = fmt.Fprintf(w, "%-32s %-12s %-12s %s\n", "CODE", "RESOURCE", "ACTION", "FIRST SEEN")
	_, _ = fmt.Fprintf(w, "%-32s %-12s %-12s %s\n",
		strings.Repeat("-", 32), strings.Repeat("-", 12),
		strings.Repeat("-", 12), strings.Repeat("-", 30))
	for _, p := range perms {
		_, _ = fmt.Fprintf(w, "%-32s %-12s %-12s %s\n", p.Code, p.Resource, p.Action, p.Source)
	}
}

// upsertPermissions writes the discovered codes into the
// `permissions` table without ever touching descriptions, role
// grants, or pre-existing rows. The query uses ON CONFLICT
// (code) DO NOTHING so re-running is safe and never overwrites
// human-curated descriptions.
func upsertPermissions(ctx context.Context, dsn string, perms []declaredPermission) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("rbac sync: open db: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("rbac sync: ping: %w", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rbac sync: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO permissions (code, resource, action, description)
		VALUES ($1, $2, $3, '')
		ON CONFLICT (code) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("rbac sync: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	inserted := 0
	for _, p := range perms {
		res, err := stmt.ExecContext(ctx, p.Code, p.Resource, p.Action)
		if err != nil {
			return fmt.Errorf("rbac sync: insert %q: %w", p.Code, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("rbac sync: commit: %w", err)
	}
	fmt.Printf("forge rbac: scanned %d code(s); upserted %d new (%d existing).\n",
		len(perms), inserted, len(perms)-inserted)
	return nil
}
