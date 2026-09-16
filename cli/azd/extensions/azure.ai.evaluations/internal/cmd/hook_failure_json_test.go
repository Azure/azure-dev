// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The hook runs before Args and RunE, so nothing below it can answer for it.
// `--cwd` naming a directory that is not there fails there, and reached a caller
// who had asked for json as prose -- the one thing `-o json` promises not to do.
//
// Driven through the real tree with a real bad flag. Replacing the hook from
// here would prove nothing: reportFailuresAsJSON has already wrapped the one the
// root was built with, so a replacement is not the thing under test.
func TestAHookFailureIsStillAnsweredAsJSON(t *testing.T) {
	root := NewRootCommand()

	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&bytes.Buffer{})

	missing := filepath.Join(t.TempDir(), "not-a-directory")
	root.SetArgs([]string{"list", "--cwd", missing, "-o", "json"})
	err := root.Execute()
	require.Error(t, err, "a --cwd that is not there has to fail")

	var doc jsonError
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &doc),
		"a hook failure under -o json must leave one parseable document on stdout, got %q",
		stdout.String())
	require.NotEmpty(t, doc.Error.Message)

	// One answer, not two. Answering in the hook as well as in the wrapper put
	// the same document on stdout twice, which only os.Exit was hiding.
	require.Equal(t, 1, bytes.Count(stdout.Bytes(), []byte(`"error"`)),
		"exactly one document belongs on stdout, got %q", stdout.String())
}

// The guard above can only fail if the hook is actually wrapped, so pin the
// wrapping itself: the bug was a missing call, and a test that calls failAs by
// hand cannot see it come back.
func TestHookFailuresAreRoutedThroughFailAs(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "output.go", nil, 0)
	require.NoError(t, err)

	wrapped := false
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "reportFailuresAsJSON" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if ok && sel.Sel.Name == "PersistentPreRunE" {
					wrapped = true
				}
			}
			return true
		})
		return false
	})

	require.True(t, wrapped,
		"reportFailuresAsJSON must wrap PersistentPreRunE; without it a hook failure "+
			"under -o json reaches the caller as prose")
}
