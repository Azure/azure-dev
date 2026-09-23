// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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

// typeDefinition is a package-local named type declaration and the file that
// declared it, so its underlying type is resolved with the right import aliases.
type typeDefinition struct {
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
	payloadTypeNames    map[string]bool
	namedTypes          map[string]typeDefinition
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
		collectNamedTypes(pkg)
		collectPayloadTypeNames(pkg)
	}

	packagesByImportPath := indexPackagesByImportPath(packages)
	expandChainedPayloadTypeNames(packages, packagesByImportPath)

	var usages []telemetryUsage
	for _, pkg := range packages {
		for _, source := range pkg.files {
			ast.Inspect(source.file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CompositeLit:
					switch {
					case isTelemetryPayloadType(value.Type, source):
						payloadUsages, payloadDiagnostics := scanPayloadAttributes(
							fset, extensionRoot, source, pkg, value)
						usages = append(usages, payloadUsages...)
						diagnostics = append(diagnostics, payloadDiagnostics...)
					case isTelemetryPayloadContainer(value.Type, source) ||
						isNamedPayloadContainer(value.Type, pkg):
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: build each telemetry payload as a single keyed literal, "+
								"not inside a slice, array, or map",
							displayPath(extensionRoot, source.path),
							fset.Position(value.Pos()).Line))
						// The container's entries elide their type; they are already
						// reported here, so stop before the type-elided rule below.
						return false
					case value.Type == nil && compositeLiteralHasAttributesKey(value):
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: build each telemetry payload as a single keyed literal of the "+
								"concrete payload type, not a type-elided literal, so attribute "+
								"keys stay discoverable",
							displayPath(extensionRoot, source.path),
							fset.Position(value.Pos()).Line))
					}
				case *ast.GenDecl:
					diagnostics = append(diagnostics, scanPayloadTypeDeclarations(
						fset, extensionRoot, source, pkg, value)...)
				case *ast.CallExpr:
					if isTelemetrySinkCall(value, source) && !sinkPayloadIsInlineLiteral(value, source) {
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: pass the telemetry payload to ReportUsage as an inline keyed "+
								"literal so its attribute keys are scanned; a variable, parameter, or "+
								"decoded payload emits keys that governance cannot see",
							displayPath(extensionRoot, source.path),
							fset.Position(value.Pos()).Line))
					}
				case *ast.SelectorExpr:
					if value.Sel.Name == "Attributes" || value.Sel.Name == "GetAttributes" {
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: set telemetry attribute keys only inside the payload literal; "+
								"reading or assigning .%s in extension code hides keys from "+
								"governance (rename unrelated fields)",
							displayPath(extensionRoot, source.path),
							fset.Position(value.Pos()).Line,
							value.Sel.Name))
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

// compositeLiteralHasAttributesKey reports whether a composite literal has an
// Attributes entry. A type-elided literal with such an entry (for example
// []Usage{{Attributes: ...}}) hides its element type, so the scanner rejects it
// rather than guessing which concrete type is being built.
func compositeLiteralHasAttributesKey(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if name, ok := keyValue.Key.(*ast.Ident); ok && name.Name == "Attributes" {
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

// isTelemetrySinkCall reports whether a call is the azd telemetry sink,
// TelemetryServiceClient.ReportUsage. The receiver type is not resolved (the
// scanner avoids go/types), so the distinctive method name is matched in a file
// that imports an azdext package, which every real sink call does.
func isTelemetrySinkCall(call *ast.CallExpr, source *sourceFile) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "ReportUsage" {
		return false
	}
	return fileImportsAzdext(source)
}

// fileImportsAzdext reports whether the file imports an azdext package, including
// through a dot import, so a ReportUsage call can be recognized as the sink.
func fileImportsAzdext(source *sourceFile) bool {
	for _, importPath := range source.imports {
		if importPath == azdextPackagePath || importPath == azdextV1BetaPackagePath {
			return true
		}
	}
	for importPath := range source.dotImports {
		if importPath == azdextPackagePath || importPath == azdextV1BetaPackagePath {
			return true
		}
	}
	return false
}

// sinkPayloadIsInlineLiteral reports whether the payload argument of a telemetry
// sink call is an inline telemetry payload literal, optionally address-of. That
// literal is the only construction the scanner reads keys from; any other form,
// such as a variable, parameter, conversion, or decoded value, hides its keys, so
// the sink call is rejected instead.
func sinkPayloadIsInlineLiteral(call *ast.CallExpr, source *sourceFile) bool {
	if len(call.Args) < 2 {
		return false
	}
	expression := call.Args[1]
	if unary, ok := expression.(*ast.UnaryExpr); ok && unary.Op == token.AND {
		expression = unary.X
	}
	literal, ok := expression.(*ast.CompositeLit)
	if !ok {
		return false
	}
	return isTelemetryPayloadType(literal.Type, source)
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

// isNamedPayloadContainer reports whether a composite literal type is a
// package-local named type whose underlying type is a telemetry payload
// container (for example type Events []telemetry.Event). Such wrappers elide the
// element type on their entries, hiding attribute keys, so the scanner rejects
// them like a literal container. Each hop is resolved with the import aliases of
// the file that declared the type, and a visited set stops recursive type loops.
func isNamedPayloadContainer(expression ast.Expr, pkg *sourcePackage) bool {
	seen := map[string]bool{}
	for {
		identifier, ok := expression.(*ast.Ident)
		if !ok {
			return false
		}
		if seen[identifier.Name] {
			return false
		}
		seen[identifier.Name] = true
		definition, ok := pkg.namedTypes[identifier.Name]
		if !ok {
			return false
		}
		if isTelemetryPayloadContainer(definition.expression, definition.source) {
			return true
		}
		expression = definition.expression
	}
}

// moduleDefinition is a Go module's path and the directory that holds its go.mod.
type moduleDefinition struct {
	path      string
	directory string
}

// indexPackagesByImportPath maps each parsed package to its Go import path by
// locating the nearest enclosing go.mod. Packages without a resolvable module are
// omitted, so cross-package alias resolution simply skips them.
func indexPackagesByImportPath(packages map[string]*sourcePackage) map[string]*sourcePackage {
	byImportPath := map[string]*sourcePackage{}
	moduleCache := map[string]moduleDefinition{}
	for _, pkg := range packages {
		module, ok := moduleForDir(pkg.directory, moduleCache)
		if !ok {
			continue
		}
		importPath := module.path
		if relative, err := filepath.Rel(module.directory, pkg.directory); err == nil && relative != "." {
			importPath = module.path + "/" + filepath.ToSlash(relative)
		}
		byImportPath[importPath] = pkg
	}
	return byImportPath
}

// moduleForDir finds the nearest go.mod at or above dir and returns its module
// path. Results are cached per starting directory so repeated lookups stay cheap.
func moduleForDir(dir string, cache map[string]moduleDefinition) (moduleDefinition, bool) {
	if cached, ok := cache[dir]; ok {
		return cached, cached.path != ""
	}
	for current := dir; ; {
		data, err := os.ReadFile(filepath.Join(current, "go.mod"))
		if err == nil {
			module := moduleDefinition{path: modulePathFromGoMod(data), directory: current}
			cache[dir] = module
			return module, module.path != ""
		}
		parent := filepath.Dir(current)
		if parent == current {
			cache[dir] = moduleDefinition{}
			return moduleDefinition{}, false
		}
		current = parent
	}
}

// modulePathFromGoMod extracts the module path from go.mod content without a
// module-file dependency: the first module directive wins and any quoting is
// trimmed.
func modulePathFromGoMod(data []byte) string {
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			return strings.Trim(fields[1], "\"")
		}
	}
	return ""
}

// collectNamedTypes records package-local named type declarations (both defined
// types and aliases) with the file that declared them, so a payload container
// hidden behind a named wrapper type can be resolved and rejected.
func collectNamedTypes(pkg *sourcePackage) {
	pkg.namedTypes = map[string]typeDefinition{}
	for _, source := range pkg.files {
		for _, declaration := range source.file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				pkg.namedTypes[typeSpec.Name.Name] = typeDefinition{
					expression: typeSpec.Type,
					source:     source,
				}
			}
		}
	}
}

// collectPayloadTypeNames records package-local named types (aliases and defined
// types) whose right-hand side is directly a telemetry payload type, so payload
// literals or conversions written through the local name are rejected instead of
// silently skipped. It reads pkg.namedTypes, collected earlier, and marks each
// direct match. Chained names that reach a payload through further named types
// are marked once all packages are indexed; see expandChainedPayloadTypeNames.
func collectPayloadTypeNames(pkg *sourcePackage) {
	pkg.payloadTypeNames = map[string]bool{}
	for name, definition := range pkg.namedTypes {
		if isTelemetryPayloadType(definition.expression, definition.source) {
			pkg.payloadTypeNames[name] = true
		}
	}
}

// scanPayloadTypeDeclarations rejects a named type whose right-hand side resolves
// to a telemetry payload, whether it is an alias (type Usage = azdext.ReportUsageRequest)
// or a defined type (type Usage azdext.ReportUsageRequest). Either form lets an
// extension construct the payload under a different name (a defined type converts
// back with an explicit conversion), so it is rejected at its declaration to keep
// attribute keys discoverable. pkg.payloadTypeNames already includes chained and
// cross-package names (see expandChainedPayloadTypeNames); a direct right-hand
// side is checked as well so a local name is caught without pre-collection.
func scanPayloadTypeDeclarations(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	pkg *sourcePackage,
	declaration *ast.GenDecl,
) []string {
	if declaration.Tok != token.TYPE {
		return nil
	}
	var diagnostics []string
	for _, spec := range declaration.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		if !pkg.payloadTypeNames[typeSpec.Name.Name] && !isTelemetryPayloadType(typeSpec.Type, source) {
			continue
		}
		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s:%d: do not alias or redefine telemetry payload types (type %s); construct "+
				"payloads with the concrete payload type so attribute keys stay discoverable",
			displayPath(extensionRoot, source.path),
			fset.Position(typeSpec.Pos()).Line,
			typeSpec.Name.Name))
	}
	return diagnostics
}

// expandChainedPayloadTypeNames marks a named type as a payload type name when its
// right-hand side resolves to a telemetry payload through one or more further
// named types, whether local (type B = A, type B A) or re-exported by another
// package in the same module (type Report = shared.Usage). It runs once every
// package is indexed so cross-package hops can be followed, closing the gap where
// a chained name would otherwise construct a payload while escaping the direct
// check.
func expandChainedPayloadTypeNames(packages, packagesByImportPath map[string]*sourcePackage) {
	for _, pkg := range packages {
		for name, definition := range pkg.namedTypes {
			if pkg.payloadTypeNames[name] {
				continue
			}
			seen := map[string]bool{}
			if typeResolvesToPayload(definition.expression, definition.source, pkg, packagesByImportPath, seen) {
				pkg.payloadTypeNames[name] = true
			}
		}
	}
}

// typeResolvesToPayload reports whether a named type's right-hand side resolves to
// a telemetry payload through any chain of local or cross-package named types. A
// local identifier is followed through the package's own named types and a package
// selector through the imported package's named types, matched on the exact go.mod
// import path. The visited set, keyed by package directory or import path plus
// name, stops a type cycle from recursing forever.
func typeResolvesToPayload(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
	packagesByImportPath map[string]*sourcePackage,
	seen map[string]bool,
) bool {
	if isTelemetryPayloadType(expression, source) {
		return true
	}
	switch value := expression.(type) {
	case *ast.Ident:
		key := pkg.directory + "\x00" + value.Name
		if seen[key] {
			return false
		}
		seen[key] = true
		definition, ok := pkg.namedTypes[value.Name]
		if !ok {
			return false
		}
		return typeResolvesToPayload(definition.expression, definition.source, pkg, packagesByImportPath, seen)
	case *ast.SelectorExpr:
		packageIdentifier, ok := value.X.(*ast.Ident)
		if !ok || (packageIdentifier.Obj != nil && packageIdentifier.Obj.Kind != ast.Pkg) {
			return false
		}
		importPath, ok := source.imports[packageIdentifier.Name]
		if !ok {
			return false
		}
		declaringPackage, ok := packagesByImportPath[importPath]
		if !ok {
			return false
		}
		key := importPath + "\x00" + value.Sel.Name
		if seen[key] {
			return false
		}
		seen[key] = true
		definition, ok := declaringPackage.namedTypes[value.Sel.Name]
		if !ok {
			return false
		}
		return typeResolvesToPayload(
			definition.expression, definition.source, declaringPackage, packagesByImportPath, seen)
	}
	return false
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
