// Package gen implements `forge gen resource <Name>`, an end-to-end
// CRUD scaffolder that produces every layer goforge expects:
//
//	internal/domain/<lc>/<lc>.go            — entity + audit fields
//	internal/domain/<lc>/repository.go      — repo interface
//	internal/usecase/<lc>.go                — Create/Get/List/Update/Delete
//	internal/adapter/repository/postgres/<lc>.go — pgx impl with soft-delete
//	internal/adapter/http/dto/<lc>.go       — request/response DTOs
//	internal/adapter/http/handler/<lc>.go   — Fiber handler with the 5 routes
//	internal/usecase/<lc>_test.go           — table-driven tests
//	migrations/NNNN_create_<plural>.up.sql  — table with audit columns
//	migrations/NNNN_create_<plural>.down.sql
//
// The generator is template-driven so adding a new pattern only
// touches the templates, not Go code. Templates live in
// internal/forge/gen/templates/*.tmpl and are embedded at build time
// so the binary works without the source tree.
package gen

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode"
)

//go:embed templates/*.tmpl
var tmplFS embed.FS

//go:embed templates_admin/*.tmpl
var tmplAdminFS embed.FS

// Resource is the data we feed templates. All fields are computed
// from the resource's name; callers only need to supply Name.
type Resource struct {
	Name        string // PascalCase singular, e.g. Order
	Lower       string // lowercase singular, e.g. order
	Plural      string // lowercase plural, e.g. orders
	Module      string // Go module path, e.g. github.com/dedeez14/goforge
	MigrationID string // 0007 etc.
}

// Options tunes Generate's behaviour. Zero-value is "baseline
// templates only", matching the pre-option callsite.
type Options struct {
	// WithAdmin, when true, additionally emits the admin UI
	// companion file (internal/app/admin_<lc>.go) which declares
	// the adminui.Resource for this aggregate.
	WithAdmin bool
}

// Generate writes every template under repoRoot. files reports the
// list of created files so the caller can print them.
func Generate(repoRoot, name, modulePath string) (files []string, err error) {
	return GenerateWithOptions(repoRoot, name, modulePath, Options{})
}

// GenerateWithOptions is the option-aware form of Generate. Prefer
// this in new code; Generate is kept for backwards compatibility.
func GenerateWithOptions(repoRoot, name, modulePath string, opts Options) (files []string, err error) {
	if name == "" {
		return nil, errors.New("gen: --name is required")
	}
	if !startsUpper(name) {
		return nil, errors.New("gen: name must be PascalCase, e.g. Order")
	}

	r := Resource{
		Name:   name,
		Lower:  strings.ToLower(name),
		Plural: strings.ToLower(name) + "s",
		Module: modulePath,
	}
	r.MigrationID = nextMigration(repoRoot)

	sources := []struct {
		fs  embed.FS
		dir string
	}{
		{tmplFS, "templates"},
	}
	if opts.WithAdmin {
		sources = append(sources, struct {
			fs  embed.FS
			dir string
		}{tmplAdminFS, "templates_admin"})
	}

	for _, src := range sources {
		batch, err := renderDir(repoRoot, src.fs, src.dir, r)
		if err != nil {
			return nil, err
		}
		files = append(files, batch...)
	}
	sort.Strings(files)
	return files, nil
}

func renderDir(repoRoot string, sfs embed.FS, dir string, r Resource) ([]string, error) {
	tmpls, err := fs.ReadDir(sfs, dir)
	if err != nil {
		return nil, err
	}
	var produced []string
	for _, e := range tmpls {
		if e.IsDir() {
			continue
		}
		raw, err := sfs.ReadFile(dir + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		t, err := template.New(e.Name()).Funcs(template.FuncMap{
			"title": func(s string) string {
				if s == "" {
					return s
				}
				return string(unicode.ToUpper(rune(s[0]))) + s[1:]
			},
		}).Parse(string(raw))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}

		// Filenames use literal '__' as a path separator and
		// {{LC}} / {{PLURAL}} / {{MIG}} placeholders. We keep
		// the file-name expansion as plain string replace (rather
		// than running it through text/template) because shell
		// quoting and Go's template syntax disagree on what the
		// braces mean.
		rel := strings.TrimSuffix(e.Name(), ".tmpl")
		rel = strings.ReplaceAll(rel, "__", string(os.PathSeparator))
		rel = strings.ReplaceAll(rel, "{{LC}}", r.Lower)
		rel = strings.ReplaceAll(rel, "{{PLURAL}}", r.Plural)
		rel = strings.ReplaceAll(rel, "{{MIG}}", r.MigrationID)

		full := filepath.Join(repoRoot, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if _, err := os.Stat(full); err == nil {
			return nil, fmt.Errorf("refusing to overwrite %s", rel)
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, r); err != nil {
			return nil, fmt.Errorf("exec %s: %w", e.Name(), err)
		}
		if err := os.WriteFile(full, buf.Bytes(), 0o644); err != nil {
			return nil, err
		}
		produced = append(produced, rel)
	}
	return produced, nil
}

func startsUpper(s string) bool {
	if s == "" {
		return false
	}
	return unicode.IsUpper(rune(s[0]))
}

func nextMigration(root string) string {
	dir := filepath.Join(root, "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "0001"
	}
	max := 0
	for _, e := range entries {
		var n int
		_, _ = fmt.Sscanf(e.Name(), "%04d_", &n)
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("%04d", max+1)
}
