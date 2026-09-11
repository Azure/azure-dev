// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// importPath is what a file has to import for its `x.Name` selectors to count.
const importPath = "azureaieval/internal/messages"

// Every sentence in this package has to be spoken by something a user runs.
//
// The package exists so the whole voice of the CLI can be read in one sitting.
// A constructor nobody calls is a sentence in that reading which no user will
// ever see, and it is worse than clutter: three times now, consolidating two
// ways of saying one thing has left the losing wording behind -- still
// compiling, still reviewed, ready to be picked up by someone who finds it and
// assumes it is live. `unused` cannot catch them, because they are exported.
//
// A reference from a test does not count. A message that only a test calls is
// still a sentence no user sees, and counting tests would let the deleted
// wording be kept alive by the test written for it, which is exactly what
// happened to `evalIDKeys`.
func TestEveryMessageIsSpokenBySomethingAUserRuns(t *testing.T) {
	root := moduleRoot(t)
	pkgDir := filepath.Join(root, "internal", "messages")

	declared := exported(t, pkgDir)
	require.NotEmpty(t, declared, "the package should declare messages")

	spoken := referenced(t, root, pkgDir, false)
	fromTests := referenced(t, root, pkgDir, true)

	var silent, testOnly []string
	for name := range declared {
		switch {
		case spoken[name]:
		case fromTests[name]:
			testOnly = append(testOnly, name)
		default:
			silent = append(silent, name)
		}
	}

	require.Empty(t, silent,
		"messages nothing calls: delete them, or call them.\n%s", strings.Join(silent, "\n"))
	require.Empty(t, testOnly,
		"messages only a test calls, so no user ever sees them:\n%s", strings.Join(testOnly, "\n"))
}

// moduleRoot walks up from this package to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	require.NoError(t, err)
	for {
		// Stat rather than Glob: Glob reads its argument as a pattern, so a
		// checkout path containing a bracket answers ErrBadPattern and the
		// walk blames the repo layout for a path this test chose to build.
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "no go.mod above the messages package")
		dir = parent
	}
}

// exported collects the package-level exported names declared in dir.
//
// Functions, and the vars and consts beside them: a marker nobody prints
// is as unspoken as a constructor nobody calls.
func exported(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}

	for _, file := range parseDir(t, dir) {
		if strings.HasSuffix(file.path, "_test.go") {
			continue
		}
		for _, decl := range file.ast.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				// A method is reached through its type, not by name here.
				if d.Recv == nil && d.Name.IsExported() {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, ident := range value.Names {
						if ident.IsExported() {
							names[ident.Name] = true
						}
					}
				}
			}
		}
	}
	return names
}

// referenced collects the names selected off this package elsewhere in the
// module, from test files or from the rest of it.
func referenced(t *testing.T, root, pkgDir string, inTests bool) map[string]bool {
	t.Helper()
	used := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// An entry this test cannot read says nothing about messages, and
			// failing the invariant over it would report the wrong problem.
			return nil //nolint:nilerr // unreadable entries are not this test's business
		}
		if d.IsDir() {
			// The package's own internal tests call these bare, so they
			// contribute no selectors; an external one beside this file would,
			// and would make the check satisfy itself.
			if path == pkgDir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") != inTests {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if err != nil {
			return nil //nolint:nilerr // a file that will not parse is the compiler's to report
		}
		local, ok := localName(file)
		if !ok {
			return nil
		}
		full, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil //nolint:nilerr // a file that will not parse is the compiler's to report
		}
		ast.Inspect(full, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// Matched against the name this file bound the import to, so an
			// aliased import counts and an unrelated `messages` identifier
			// does not.
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == local {
				used[sel.Sel.Name] = true
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	return used
}

// localName is the name a file binds this package to, if it imports it.
func localName(file *ast.File) (string, bool) {
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != importPath {
			continue
		}
		if imp.Name != nil {
			// A dot import puts the names in scope unqualified, which this
			// test cannot follow; nothing in the module does it.
			if imp.Name.Name == "." || imp.Name.Name == "_" {
				return "", false
			}
			return imp.Name.Name, true
		}
		return "messages", true
	}
	return "", false
}

type parsedFile struct {
	path string
	ast  *ast.File
}

func parseDir(t *testing.T, dir string) []parsedFile {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var files []parsedFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		// ParseDir is deprecated as of Go 1.22, and reading the directory
		// here costs nothing.
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		files = append(files, parsedFile{path: path, ast: file})
	}
	return files
}
