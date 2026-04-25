package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestDefaultBundle_CoversAllErrorCodes scans the source tree for
// every code passed to errs.New / errs.NotFound / errs.Conflict /
// errs.Forbidden / errs.InvalidInput / errs.Unauthorized / errs.Wrap
// and asserts the DefaultBundle has a translation for it (in both
// supported locales). Without this, additions to the codebase that
// rely on i18n would silently fall back to English without anyone
// noticing.
//
// The test runs from pkg/i18n/. We walk up to the repo root, then
// scan internal/ and pkg/ (skipping our own package and test files
// and the scaffold templates that contain {{.Lower}} placeholders).
func TestDefaultBundle_CoversAllErrorCodes(t *testing.T) {
	root := repoRoot(t)
	codes := scanErrorCodes(t, root)
	if len(codes) == 0 {
		t.Fatalf("scanner found no error codes; check the regex / paths")
	}

	bundle := DefaultBundle()
	var missing []string
	for _, c := range codes {
		if _, ok := bundle.Lookup(c, LocaleEN); !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("DefaultBundle is missing translations for %d error code(s):\n  - %s\n\nadd them to pkg/i18n/bundle_default.go",
			len(missing), strings.Join(missing, "\n  - "))
	}

	// Mirror check: every code we register must be reachable in
	// LocaleID too, or the Indonesian translation is dead code.
	for _, c := range codes {
		if _, ok := bundle.Lookup(c, LocaleID); !ok {
			t.Errorf("code %q has English but no Indonesian translation", c)
		}
	}
}

// repoRoot walks upward from the test's working directory until it
// finds a go.mod file.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod above %q", dir)
		}
		dir = parent
	}
}

// codeCallRe matches the constructors that produce *client-facing*
// errors. errs.Wrap is intentionally excluded - it is used for
// internal failures (e.g. SQL scan errors) whose codes never reach
// the client; httpx.RespondError masks those to a generic "internal"
// payload. Including them would force translations for hundreds of
// dead codes.
var codeCallRe = regexp.MustCompile(
	`errs\.(?:New|NotFound|Conflict|Forbidden|InvalidInput|Unauthorized)\([^,)]*?,?\s*"([a-z][a-z0-9._-]*)"`,
)

// scanErrorCodes walks the repository's internal/ and pkg/ trees
// (excluding test files, our own package and template scaffolds) and
// collects every literal string passed as the `code` argument to a
// well-known errs constructor.
func scanErrorCodes(t *testing.T, root string) []string {
	t.Helper()
	seen := make(map[string]struct{})
	for _, sub := range []string{"internal", "pkg"} {
		base := filepath.Join(root, sub)
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			// Skip our own package - the bundle file is the catalogue,
			// not a caller.
			if strings.Contains(path, filepath.Join("pkg", "i18n")) {
				return nil
			}
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for _, m := range codeCallRe.FindAllStringSubmatch(string(body), -1) {
				code := m[1]
				// Skip scaffold-template placeholders; they're rendered
				// per-resource at generation time.
				if strings.Contains(code, "{{") {
					continue
				}
				seen[code] = struct{}{}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
