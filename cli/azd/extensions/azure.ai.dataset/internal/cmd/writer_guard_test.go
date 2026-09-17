// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// callPassesCommandErrWriter reports whether every call to name in file hands it
// cmd.ErrOrStderr(). Used where the bug would be a wrong argument rather than a
// wrong implementation, which a test supplying the argument itself cannot see.
func callPassesCommandErrWriter(t *testing.T, file, name string) bool {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}

	found, passes := false, true
	ast.Inspect(parsed, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := call.Fun.(*ast.Ident)
		if !ok || fn.Name != name {
			return true
		}
		found = true
		if !callArgIsCommandErrWriter(call.Args) {
			passes = false
		}
		return true
	})

	if !found {
		t.Fatalf("no call to %s in %s; this guard is pinned to nothing", name, file)
	}
	return passes
}

func callArgIsCommandErrWriter(args []ast.Expr) bool {
	for _, arg := range args {
		inner, ok := arg.(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := inner.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ErrOrStderr" {
			continue
		}
		if recv, ok := sel.X.(*ast.Ident); ok && recv.Name == "cmd" {
			return true
		}
	}
	return false
}
