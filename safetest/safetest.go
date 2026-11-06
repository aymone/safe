// Package safetest provides a test guard that forbids bare `go` statements,
// so every goroutine in a module starts through safe.Go or safe.Loop.
//
//	func TestNoBareGo(t *testing.T) { safetest.NoBareGo(t) }
package safetest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NoBareGo fails t if any non-test .go file in the module containing the
// working directory has a `go` statement. skipDirs are paths relative to the
// module root that are allowed to start goroutines.
func NoBareGo(t testing.TB, skipDirs ...string) {
	t.Helper()
	root, err := ModuleRoot()
	if err != nil {
		t.Fatal(err)
	}
	found, err := FindBareGo(root, skipDirs...)
	if err != nil {
		t.Fatalf("safetest: %v", err)
	}
	if len(found) > 0 {
		t.Fatalf("bare `go` statement(s); start goroutines with safe.Go or safe.Loop:\n%s", strings.Join(found, "\n"))
	}
}

// FindBareGo returns "file:line" for every `go` statement in non-test .go
// files under root. Hidden directories, vendor and testdata are skipped, as
// are skipDirs (relative to root). _test.go files may start goroutines.
func FindBareGo(root string, skipDirs ...string) ([]string, error) {
	skip := make(map[string]bool, len(skipDirs))
	for _, d := range skipDirs {
		skip[filepath.Join(root, d)] = true
	}

	var found []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || skip[path]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if g, ok := n.(*ast.GoStmt); ok {
				pos := fset.Position(g.Pos())
				found = append(found, fmt.Sprintf("%s:%d", pos.Filename, pos.Line))
			}
			return true
		})
		return nil
	})
	return found, err
}

// ModuleRoot walks up from the working directory to the nearest go.mod.
func ModuleRoot() (string, error) {
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
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		dir = parent
	}
}
