// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every sentence in this package has to be spoken by something.
//
// The package exists so the whole voice of the CLI can be read in one sitting.
// A constructor nobody calls is a sentence in that reading which no user will
// ever see, and it is worse than clutter: twice now, consolidating two ways of
// saying one thing has left the losing wording behind, still compiling, still
// reviewed, ready to be picked up again by someone who finds it and assumes it
// is live. `unused` cannot catch them, because they are exported.
//
// The rule is deliberately blunt -- referenced somewhere outside the package,
// or deleted. Anything genuinely written for a caller that does not exist yet
// should arrive with that caller.
func TestEveryMessageIsSpokenBySomething(t *testing.T) {
	root := moduleRoot(t)
	declared := exportedFuncs(t, filepath.Join(root, "internal", "messages"))
	require.NotEmpty(t, declared, "the package should declare messages")

	spoken := referencedNames(t, root)

	var silent []string
	for name := range declared {
		if !spoken[name] {
			silent = append(silent, name)
		}
	}

	require.Empty(t, silent,
		"messages nothing calls: delete them, or call them.\n%s",
		strings.Join(silent, "\n"))
}

// moduleRoot walks up from this package to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	require.NoError(t, err)
	for range 8 {
		if _, err := filepath.Glob(filepath.Join(dir, "go.mod")); err == nil {
			if matches, _ := filepath.Glob(filepath.Join(dir, "go.mod")); len(matches) > 0 {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "walked past the filesystem root")
		dir = parent
	}
	t.Fatal("no go.mod above the messages package")
	return ""
}

// exportedFuncs collects the package-level exported functions declared in dir.
func exportedFuncs(t *testing.T, dir string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	require.NoError(t, err)

	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			// A test file's helpers are not part of the voice.
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				// Methods hang off a type and are reached through it.
				if !ok || fn.Recv != nil || !fn.Name.IsExported() {
					continue
				}
				names[fn.Name.Name] = true
			}
		}
	}
	return names
}

// referencedNames collects every `messages.X` selector used outside the package.
func referencedNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	used := map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			// A file this package cannot parse is not this test's business;
			// the build already fails on it.
			return nil //nolint:nilerr // parse failures are the compiler's to report
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "messages" {
				used[sel.Sel.Name] = true
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	return used
}
