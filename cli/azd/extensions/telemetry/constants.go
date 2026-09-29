// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"go/ast"
	"go/token"
	"strconv"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

func collectConstants(pkg *sourcePackage) {
	pkg.constants = map[string][]constDefinition{}
	pkg.objectConstants = map[*parserObject]constDefinition{}

	for _, source := range pkg.files {
		for _, declaration := range source.file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			collectConstantDeclaration(pkg, source, gen, true)
		}
		ast.Inspect(source.file, func(node ast.Node) bool {
			gen, ok := node.(*ast.GenDecl)
			if ok && gen.Tok == token.CONST {
				collectConstantDeclaration(pkg, source, gen, false)
			}
			return true
		})
	}
}

func collectConstantDeclaration(
	pkg *sourcePackage,
	source *sourceFile,
	gen *ast.GenDecl,
	packageScope bool,
) {
	var previousValues []ast.Expr
	for _, spec := range gen.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if len(valueSpec.Values) > 0 {
			previousValues = valueSpec.Values
		}
		for i, name := range valueSpec.Names {
			if len(previousValues) == 0 {
				continue
			}
			valueIndex := min(i, len(previousValues)-1)
			definition := constDefinition{
				expression: previousValues[valueIndex],
				source:     source,
			}
			if name.Obj != nil {
				pkg.objectConstants[name.Obj] = definition
			}
			if packageScope {
				pkg.constants[name.Name] = append(pkg.constants[name.Name], definition)
			}
		}
	}
}

type constantResolution struct {
	objects map[*parserObject]bool
	names   map[string]bool
}

func resolveStringConstant(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
	resolving *constantResolution,
) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(value.Value)
		return unquoted, err == nil
	case *ast.ParenExpr:
		return resolveStringConstant(value.X, source, pkg, resolving)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := resolveStringConstant(value.X, source, pkg, resolving)
		right, rightOK := resolveStringConstant(value.Y, source, pkg, resolving)
		return left + right, leftOK && rightOK
	case *ast.CallExpr:
		name, ok := value.Fun.(*ast.Ident)
		if !ok ||
			name.Name != "string" ||
			name.Obj != nil ||
			pkg.packageDeclarations["string"] ||
			len(value.Args) != 1 {
			return "", false
		}
		return resolveStringConstant(value.Args[0], source, pkg, resolving)
	case *ast.SelectorExpr:
		alias, ok := value.X.(*ast.Ident)
		if !ok ||
			alias.Obj != nil && alias.Obj.Kind != ast.Pkg ||
			source.imports[alias.Name] != tracingFieldsPackagePath ||
			value.Sel.Name != "ExtensionAttributePrefix" {
			return "", false
		}
		return fields.ExtensionAttributePrefix, true
	case *ast.Ident:
		if value.Obj != nil {
			if value.Obj.Kind != ast.Con {
				return "", false
			}
			definition, ok := pkg.objectConstants[value.Obj]
			if !ok {
				return "", false
			}
			resolving = ensureConstantResolution(resolving)
			if resolving.objects[value.Obj] {
				return "", false
			}
			resolving.objects[value.Obj] = true
			defer delete(resolving.objects, value.Obj)
			return resolveStringConstant(definition.expression, definition.source, pkg, resolving)
		}

		definitions := pkg.constants[value.Name]
		if len(definitions) == 0 {
			return "", false
		}
		resolving = ensureConstantResolution(resolving)
		if resolving.names[value.Name] {
			return "", false
		}
		resolving.names[value.Name] = true
		defer delete(resolving.names, value.Name)

		var resolved string
		for i, definition := range definitions {
			current, ok := resolveStringConstant(definition.expression, definition.source, pkg, resolving)
			if !ok || (i > 0 && current != resolved) {
				return "", false
			}
			resolved = current
		}
		return resolved, true
	default:
		return "", false
	}
}

func resolveBoolConstant(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
	resolving *constantResolution,
) (bool, bool) {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return resolveBoolConstant(value.X, source, pkg, resolving)
	case *ast.UnaryExpr:
		if value.Op != token.NOT {
			return false, false
		}
		resolved, ok := resolveBoolConstant(value.X, source, pkg, resolving)
		return !resolved, ok
	case *ast.Ident:
		if value.Obj != nil {
			if value.Obj.Kind != ast.Con {
				return false, false
			}
			definition, ok := pkg.objectConstants[value.Obj]
			if !ok {
				return false, false
			}
			resolving = ensureConstantResolution(resolving)
			if resolving.objects[value.Obj] {
				return false, false
			}
			resolving.objects[value.Obj] = true
			defer delete(resolving.objects, value.Obj)
			return resolveBoolConstant(definition.expression, definition.source, pkg, resolving)
		}

		if !pkg.packageDeclarations[value.Name] {
			switch value.Name {
			case "true":
				return true, true
			case "false":
				return false, true
			}
		}

		definitions := pkg.constants[value.Name]
		if len(definitions) == 0 {
			return false, false
		}
		resolving = ensureConstantResolution(resolving)
		if resolving.names[value.Name] {
			return false, false
		}
		resolving.names[value.Name] = true
		defer delete(resolving.names, value.Name)

		var resolved bool
		for i, definition := range definitions {
			current, ok := resolveBoolConstant(definition.expression, definition.source, pkg, resolving)
			if !ok || (i > 0 && current != resolved) {
				return false, false
			}
			resolved = current
		}
		return resolved, true
	default:
		return false, false
	}
}

func ensureConstantResolution(resolving *constantResolution) *constantResolution {
	if resolving != nil {
		return resolving
	}
	return &constantResolution{
		objects: map[*parserObject]bool{},
		names:   map[string]bool{},
	}
}
