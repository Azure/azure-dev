// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A filter over a list you can already see is furniture. On the two-option
// Traces or Dataset picker it also misleads, because a search row implies there
// is more to find than the two lines above it.
func TestFilteringIsOfferedOnlyAboveFiveChoices(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5} {
		require.NotNil(t, filteringFor(n))
		assert.False(t, *filteringFor(n), "%d choices need no search row", n)
	}
	for _, n := range []int{6, 7, 40} {
		require.NotNil(t, filteringFor(n))
		assert.True(t, *filteringFor(n), "%d choices are worth filtering", n)
	}
}

// The rule is only worth having if every picker follows it, and a new picker is
// written by copying an old one. Left to review, the copy that forgot would
// read exactly like the ones that did not.
//
// Asked of the source rather than of behaviour: the host renders the prompt, so
// there is nothing to observe here, and an unset field means "whatever the host
// defaults to" -- which is the state this rule exists to replace.
func TestEveryPickerDecidesWhetherToOfferFiltering(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fs.FileInfo) bool { return true }, 0)
	require.NoError(t, err)

	var missing []string
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isPickerOptions(lit) {
					return true
				}
				if !setsField(lit, "EnableFiltering") {
					missing = append(missing, filepath.Base(name)+":"+
						fset.Position(lit.Pos()).String())
				}
				return true
			})
		}
	}

	assert.Empty(t, missing,
		"these pickers leave filtering to the host; pass EnableFiltering: "+
			"filteringFor(len(choices)):\n%s", strings.Join(missing, "\n"))
}

// isPickerOptions reports an azdext.SelectOptions or MultiSelectOptions literal.
func isPickerOptions(lit *ast.CompositeLit) bool {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "azdext" {
		return false
	}
	return sel.Sel.Name == "SelectOptions" || sel.Sel.Name == "MultiSelectOptions"
}

// setsField reports whether a struct literal names a field.
func setsField(lit *ast.CompositeLit, field string) bool {
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == field {
			return true
		}
	}
	return false
}
