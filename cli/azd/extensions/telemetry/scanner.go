// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// Well-known azd packages whose telemetry payload types first-party extensions
// construct. An extension records usage by building one of these payloads and
// handing it to the azd host, so every emitted attribute key lives inside a
// payload composite literal.
const (
	azdextPackagePath           = "github.com/azure/azure-dev/cli/azd/pkg/azdext"
	azdextV1BetaPackagePath     = "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	foundryTelemetryPackagePath = "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	tracingFieldsPackagePath    = "github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	otelAttributePackagePath    = "go.opentelemetry.io/otel/attribute"
)

// telemetryUsage is one attribute key discovered inside a telemetry payload literal.
type telemetryUsage struct {
	extension string
	key       string
	path      string
	line      int
}

type sourceFile struct {
	path       string
	file       *ast.File
	imports    map[string]string
	dotImports map[string]bool
}

type constDefinition struct {
	expression ast.Expr
	source     *sourceFile
}

// parserObject preserves parser-local lexical identity without loading every nested extension module.
type parserObject = ast.Object //nolint:staticcheck // go/types would require loading extension dependencies.

type sourcePackage struct {
	directory           string
	files               []*sourceFile
	constants           map[string][]constDefinition
	objectConstants     map[*parserObject]constDefinition
	packageDeclarations map[string]bool
	payloadAliases      map[string]bool
}

// scanExtensionTelemetry parses first-party extension source and returns every
// telemetry attribute key found in a payload literal, plus diagnostics for
// payloads whose keys cannot be verified statically. It parses source only; it
// never executes extension code.
func scanExtensionTelemetry(extensionRoot string) ([]telemetryUsage, []string) {
	fset := token.NewFileSet()
	packages := map[string]*sourcePackage{}
	var diagnostics []string

	walkErr := filepath.WalkDir(extensionRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin", "node_modules", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s: failed to parse extension source: %v", filepath.ToSlash(path), parseErr))
			return nil
		}

		source := &sourceFile{
			path:       path,
			file:       file,
			imports:    importAliases(file),
			dotImports: dotImports(file),
		}
		key := filepath.Dir(path) + "\x00" + file.Name.Name
		pkg := packages[key]
		if pkg == nil {
			pkg = &sourcePackage{directory: filepath.Dir(path)}
			packages[key] = pkg
		}
		pkg.files = append(pkg.files, source)
		return nil
	})
	if walkErr != nil {
		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s: failed to scan extension source: %v", filepath.ToSlash(extensionRoot), walkErr))
	}

	for _, pkg := range packages {
		collectPackageDeclarations(pkg)
		collectConstants(pkg)
		collectPayloadAliases(pkg)
	}

	var usages []telemetryUsage
	for _, pkg := range packages {
		for _, source := range pkg.files {
			importsTelemetry := fileImportsTelemetryPackage(source)
			ast.Inspect(source.file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CompositeLit:
					switch {
					case isTelemetryPayloadType(value.Type, source):
						payloadUsages, payloadDiagnostics := scanPayloadAttributes(
							fset, extensionRoot, source, pkg, value)
						usages = append(usages, payloadUsages...)
						diagnostics = append(diagnostics, payloadDiagnostics...)
					case isTelemetryPayloadContainer(value.Type, source):
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: build each telemetry payload as a single keyed literal, "+
								"not inside a slice, array, or map",
							displayPath(extensionRoot, source.path),
							fset.Position(value.Pos()).Line))
					case isTelemetryPayloadAlias(value.Type, pkg):
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: construct telemetry payloads with the concrete payload type, "+
								"not a local type alias, so attribute keys stay discoverable",
							displayPath(extensionRoot, source.path),
							fset.Position(value.Pos()).Line))
					}
				case *ast.AssignStmt:
					if importsTelemetry {
						diagnostics = append(diagnostics,
							scanAttributeMutation(fset, extensionRoot, source, value)...)
					}
				}
				return true
			})
		}
	}

	return deduplicateTelemetryUsages(usages), deduplicateStrings(diagnostics)
}

// scanPayloadAttributes extracts attribute keys from a telemetry payload literal.
// The contract is deliberately narrow: Attributes must be an inline map literal
// whose keys are string literals or same-package compile-time constants. Anything
// else fails closed with a diagnostic so keys cannot be hidden from governance.
func scanPayloadAttributes(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	pkg *sourcePackage,
	literal *ast.CompositeLit,
) ([]telemetryUsage, []string) {
	relativePath := displayPath(extensionRoot, source.path)
	position := fset.Position(literal.Pos())

	if compositeLiteralHasUnkeyedElements(literal) {
		return nil, []string{fmt.Sprintf(
			"%s:%d: build telemetry payloads with keyed fields so Attributes can be found",
			relativePath, position.Line)}
	}

	attributes, ok := payloadAttributeMap(literal)
	if !ok {
		return nil, nil
	}

	mapLiteral, ok := attributes.(*ast.CompositeLit)
	if !ok {
		return nil, []string{fmt.Sprintf(
			"%s:%d: telemetry Attributes must be an inline map literal so keys are discoverable",
			relativePath, position.Line)}
	}

	extension := extensionName(extensionRoot, source.path)
	var usages []telemetryUsage
	var diagnostics []string
	for _, element := range mapLiteral.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s:%d: telemetry Attributes must use explicit key: value entries",
				relativePath, fset.Position(element.Pos()).Line))
			continue
		}
		key, ok := resolveStringConstant(keyValue.Key, source, pkg, nil)
		if !ok {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s:%d: telemetry attribute key must be a string literal or same-package constant",
				relativePath, fset.Position(keyValue.Key.Pos()).Line))
			continue
		}
		usages = append(usages, telemetryUsage{
			extension: extension,
			key:       key,
			path:      relativePath,
			line:      fset.Position(keyValue.Key.Pos()).Line,
		})
	}
	return usages, diagnostics
}

// payloadAttributeMap returns the Attributes field value of a keyed payload
// literal. An absent field or an explicit Attributes: nil reports no keys.
func payloadAttributeMap(literal *ast.CompositeLit) (ast.Expr, bool) {
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := keyValue.Key.(*ast.Ident)
		if !ok || name.Name != "Attributes" {
			continue
		}
		if identifier, ok := keyValue.Value.(*ast.Ident); ok && identifier.Name == "nil" {
			return nil, false
		}
		return keyValue.Value, true
	}
	return nil, false
}

func compositeLiteralHasUnkeyedElements(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		if _, ok := element.(*ast.KeyValueExpr); !ok {
			return true
		}
	}
	return false
}

// isTelemetryPayloadType reports whether a composite literal type is a telemetry
// payload: a foundry telemetry Event or an azdext ReportUsageRequest.
func isTelemetryPayloadType(expression ast.Expr, source *sourceFile) bool {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		alias, ok := value.X.(*ast.Ident)
		if !ok || (alias.Obj != nil && alias.Obj.Kind != ast.Pkg) {
			return false
		}
		return telemetryPayloadName(source.imports[alias.Name], value.Sel.Name)
	case *ast.Ident:
		for importPath := range source.dotImports {
			if telemetryPayloadName(importPath, value.Name) {
				return true
			}
		}
	}
	return false
}

func telemetryPayloadName(importPath, typeName string) bool {
	switch typeName {
	case "Event":
		return importPath == foundryTelemetryPackagePath
	case "ReportUsageRequest":
		return importPath == azdextPackagePath || importPath == azdextV1BetaPackagePath
	}
	return false
}

// isTelemetryPayloadContainer reports whether a composite literal type is a
// slice, array, map, or pointer whose element is a telemetry payload. Containers
// elide the element type on their entries, which would hide keys, so the scanner
// rejects them rather than guessing.
func isTelemetryPayloadContainer(expression ast.Expr, source *sourceFile) bool {
	switch value := expression.(type) {
	case *ast.ArrayType:
		return isTelemetryPayloadType(value.Elt, source) || isTelemetryPayloadContainer(value.Elt, source)
	case *ast.MapType:
		return isTelemetryPayloadType(value.Value, source) || isTelemetryPayloadContainer(value.Value, source)
	case *ast.StarExpr:
		return isTelemetryPayloadType(value.X, source) || isTelemetryPayloadContainer(value.X, source)
	}
	return false
}

// isTelemetryPayloadAlias reports whether a composite literal type is a local
// type alias (type X = telemetry.Event) that resolves to a telemetry payload.
// Such aliases would otherwise hide attribute keys from the type-based recognizer.
func isTelemetryPayloadAlias(expression ast.Expr, pkg *sourcePackage) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && pkg.payloadAliases[identifier.Name]
}

// collectPayloadAliases records local type aliases whose right-hand side is a
// telemetry payload type, so payload literals written through the alias name are
// rejected instead of silently skipped.
func collectPayloadAliases(pkg *sourcePackage) {
	pkg.payloadAliases = map[string]bool{}
	for _, source := range pkg.files {
		for _, declaration := range source.file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok || typeSpec.Assign == token.NoPos {
					continue
				}
				if isTelemetryPayloadType(typeSpec.Type, source) {
					pkg.payloadAliases[typeSpec.Name.Name] = true
				}
			}
		}
	}
}

// scanAttributeMutation rejects post-construction writes to a telemetry payload's
// Attributes (x.Attributes[key] = ... or x.Attributes = ...). Keys must be
// declared inline in the payload literal so governance can see them.
func scanAttributeMutation(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	assignment *ast.AssignStmt,
) []string {
	var diagnostics []string
	for _, target := range assignment.Lhs {
		selector := attributesSelector(target)
		if selector == nil {
			continue
		}
		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s:%d: declare telemetry Attributes inline in the payload literal; "+
				"assigning them after construction hides keys from governance",
			displayPath(extensionRoot, source.path),
			fset.Position(selector.Pos()).Line))
	}
	return diagnostics
}

func attributesSelector(expression ast.Expr) *ast.SelectorExpr {
	switch value := expression.(type) {
	case *ast.IndexExpr:
		return attributesSelector(value.X)
	case *ast.CallExpr:
		if selector, ok := value.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "GetAttributes" {
			return selector
		}
	case *ast.SelectorExpr:
		if value.Sel.Name == "Attributes" {
			return value
		}
	}
	return nil
}

func fileImportsTelemetryPackage(source *sourceFile) bool {
	for _, importPath := range source.imports {
		if isTelemetryPayloadPackage(importPath) {
			return true
		}
	}
	for importPath := range source.dotImports {
		if isTelemetryPayloadPackage(importPath) {
			return true
		}
	}
	return false
}

func isTelemetryPayloadPackage(importPath string) bool {
	return importPath == foundryTelemetryPackagePath ||
		importPath == azdextPackagePath ||
		importPath == azdextV1BetaPackagePath
}

func collectPackageDeclarations(pkg *sourcePackage) {
	pkg.packageDeclarations = map[string]bool{}
	for _, source := range pkg.files {
		for _, declaration := range source.file.Decls {
			switch value := declaration.(type) {
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					switch typed := spec.(type) {
					case *ast.ValueSpec:
						for _, name := range typed.Names {
							pkg.packageDeclarations[name.Name] = true
						}
					case *ast.TypeSpec:
						pkg.packageDeclarations[typed.Name.Name] = true
					}
				}
			case *ast.FuncDecl:
				pkg.packageDeclarations[value.Name.Name] = true
			}
		}
	}
}

func importAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || spec.Name != nil && (spec.Name.Name == "." || spec.Name.Name == "_") {
			continue
		}
		alias := filepath.Base(importPath)
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		aliases[alias] = importPath
	}
	return aliases
}

func dotImports(file *ast.File) map[string]bool {
	imports := map[string]bool{}
	for _, spec := range file.Imports {
		if spec.Name == nil || spec.Name.Name != "." {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err == nil {
			imports[importPath] = true
		}
	}
	return imports
}

func deduplicateTelemetryUsages(usages []telemetryUsage) []telemetryUsage {
	seen := map[string]bool{}
	result := make([]telemetryUsage, 0, len(usages))
	for _, usage := range usages {
		key := fmt.Sprintf("%s\x00%s\x00%d\x00%s", usage.path, usage.extension, usage.line, usage.key)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, usage)
	}
	return result
}

func deduplicateStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func extensionName(root, path string) string {
	relative := displayPath(root, path)
	return strings.Split(relative, "/")[0]
}

func displayPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}
