// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// legacyUnmarkedBetaSurface lists public SDK names that expose v1beta contracts without "Beta" in their name.
// Compose and Copilot predate contract versioning, CommandResult predates this rule, and
// ServiceTargetPreviewEnvelope is the deprecated beta envelope kept after deployment preview graduated.
// Do not add entries: name new beta SDK surface with "Beta" so extension authors can see they depend on preview APIs.
var legacyUnmarkedBetaSurface = []string{
	"AzdClient.CommandResult",
	"AzdClient.Compose",
	"AzdClient.Copilot",
	"ServiceTargetPreviewEnvelope",
}

func TestBetaSDKSurfaceIsNamedBeta(t *testing.T) {
	violations := unmarkedBetaSurface(t, ".")
	require.Empty(t, violations,
		"public pkg/azdext names that expose contracts/v1beta must contain \"Beta\" "+
			"(for example FooBeta() or WithBetaFoo); see docs/extensions/contract-versioning.md#beta-sdk-naming")
}

// unmarkedBetaSurface returns exported types, functions, and methods in dir whose public shape references
// contracts/v1beta, directly or through another package-local type, but whose name lacks "Beta".
func unmarkedBetaSurface(t *testing.T, dir string) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)

	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		require.NoError(t, err)
		files = append(files, file)
	}

	typeSpecs := map[string]*ast.TypeSpec{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.TYPE {
				for _, spec := range gen.Specs {
					typeSpec := spec.(*ast.TypeSpec)
					typeSpecs[typeSpec.Name.Name] = typeSpec
				}
			}
		}
	}

	// A local type is beta when its public shape references v1beta or another beta local type.
	betaTypes := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, spec := range typeSpecs {
			if !betaTypes[name] && referencesBeta(spec.Type, betaTypes) {
				betaTypes[name] = true
				changed = true
			}
		}
	}

	var violations []string
	report := func(name, qualified string) {
		if !strings.Contains(name, "Beta") && !slices.Contains(legacyUnmarkedBetaSurface, qualified) {
			violations = append(violations, qualified)
		}
	}

	for name := range betaTypes {
		if ast.IsExported(name) {
			report(name, name)
		}
	}

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() || !referencesBeta(fn.Type, betaTypes) {
				continue
			}

			if fn.Recv == nil {
				report(fn.Name.Name, fn.Name.Name)
				continue
			}

			receiver := receiverTypeName(fn.Recv.List[0].Type)
			// Methods on unexported or beta-named types are covered by their receiver.
			if !ast.IsExported(receiver) || betaTypes[receiver] || strings.Contains(receiver, "Beta") {
				continue
			}
			if slices.Contains(legacyUnmarkedBetaSurface, receiver) {
				continue
			}
			report(fn.Name.Name, receiver+"."+fn.Name.Name)
		}
	}

	slices.Sort(violations)
	return violations
}

// referencesBeta reports whether expr names a v1beta symbol or a beta local type. Unexported struct fields are
// implementation details and do not make a type part of the beta surface.
func referencesBeta(expr ast.Node, betaTypes map[string]bool) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if found {
			return false
		}
		switch n := node.(type) {
		case *ast.StructType:
			for _, field := range n.Fields.List {
				if (len(field.Names) == 0 || field.Names[0].IsExported()) && referencesBeta(field.Type, betaTypes) {
					found = true
				}
			}
			return false
		case *ast.SelectorExpr:
			if pkg, ok := n.X.(*ast.Ident); ok && pkg.Name == "v1beta" {
				found = true
			}
		case *ast.Ident:
			if betaTypes[n.Name] {
				found = true
			}
		}
		return !found
	})
	return found
}

func receiverTypeName(expr ast.Expr) string {
	for {
		switch e := expr.(type) {
		case *ast.StarExpr:
			expr = e.X
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}
