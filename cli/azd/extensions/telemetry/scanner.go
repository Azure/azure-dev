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
	directory             string
	files                 []*sourceFile
	constants             map[string][]constDefinition
	objectConstants       map[*parserObject]constDefinition
	packageDeclarations   map[string]bool
	payloadAliases        map[string]bool
	namedTypes            map[string]typeDefinition
	payloadReturningFuncs map[string]bool
	packagePayloadObjects map[*parserObject]bool
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
		collectPayloadReturningFuncs(pkg)
		collectPackagePayloadObjects(pkg)
		collectPayloadAliases(pkg)
	}

	packagesByImportPath := indexPackagesByImportPath(packages)

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
					case isTelemetryPayloadAlias(value.Type, pkg):
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: construct telemetry payloads with the concrete payload type, "+
								"not a local type alias, so attribute keys stay discoverable",
							displayPath(extensionRoot, source.path),
							fset.Position(value.Pos()).Line))
					case isCrossPackagePayloadAlias(value.Type, source, packagesByImportPath):
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: construct telemetry payloads with the concrete payload type, "+
								"not a re-exported payload alias from another package, so attribute "+
								"keys stay discoverable",
							displayPath(extensionRoot, source.path),
							fset.Position(value.Pos()).Line))
					}
				case *ast.FuncDecl:
					diagnostics = append(diagnostics, scanAttributeMutations(
						fset, extensionRoot, source, pkg, value.Recv, value.Type, value.Body)...)
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

// isTelemetryPayloadAlias reports whether a composite literal type is a local
// type alias (type X = telemetry.Event) that resolves to a telemetry payload.
// Such aliases would otherwise hide attribute keys from the type-based recognizer.
func isTelemetryPayloadAlias(expression ast.Expr, pkg *sourcePackage) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && pkg.payloadAliases[identifier.Name]
}

// isCrossPackagePayloadAlias reports whether a composite literal type is a
// payload alias re-exported by another package in the same module (shared.Usage
// where package shared declares type Usage = azdext.ReportUsageRequest). The
// selector's package identifier is resolved to an import path and matched against
// the parsed packages, so the re-exported alias is rejected like a local one
// instead of silently hiding attribute keys. Only packages parsed under the
// scanned root and reachable through a go.mod are resolved; the exact import-path
// match avoids flagging an unrelated same-named type in another module.
func isCrossPackagePayloadAlias(
	expression ast.Expr,
	source *sourceFile,
	packagesByImportPath map[string]*sourcePackage,
) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageIdentifier, ok := selector.X.(*ast.Ident)
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
	return declaringPackage.payloadAliases[selector.Sel.Name]
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
	for _, line := range strings.Split(string(data), "\n") {
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

// collectPayloadReturningFuncs records package-level functions (without a
// receiver) whose first result is a telemetry payload, so a local initialized
// from such a call -- req := newRequest() -- is tracked and a later
// req.Attributes write is still rejected. The result type is resolved with the
// import aliases of the file that declared the function, because callers may
// live in another file of the same package. Only the first result is considered,
// matching the assignment's Rhs[0] -> Lhs[0] binding.
func collectPayloadReturningFuncs(pkg *sourcePackage) {
	pkg.payloadReturningFuncs = map[string]bool{}
	for _, source := range pkg.files {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Type.Results == nil {
				continue
			}
			results := function.Type.Results.List
			if len(results) == 0 {
				continue
			}
			if isPayloadTypeExpression(results[0].Type, source) {
				pkg.payloadReturningFuncs[function.Name.Name] = true
			}
		}
	}
}

// collectPackagePayloadObjects records the binding identity of package-scope
// variables bound to a telemetry payload (var req = &azdext.ReportUsageRequest{}),
// so a mutation of their Attributes inside any function of the same file is
// rejected like a local payload instead of slipping past the empty-literal scan.
// The parser resolves references only within a file, so a payload variable read
// from another file of the package stays outside this guard.
func collectPackagePayloadObjects(pkg *sourcePackage) {
	pkg.packagePayloadObjects = map[*parserObject]bool{}
	for _, source := range pkg.files {
		for _, declaration := range source.file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				if valueSpec, ok := spec.(*ast.ValueSpec); ok {
					addPayloadValueSpecObjects(valueSpec, source, pkg, pkg.packagePayloadObjects)
				}
			}
		}
	}
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

// scanAttributeMutations rejects post-construction access to a telemetry
// payload's Attributes, whether a write (req.Attributes[key] = ...,
// req.Attributes = ...), a getter mutation (req.GetAttributes()[key] = ...), a
// read that aliases the map (attrs := req.Attributes), or a copy of the payload
// itself (alias := req). It first resolves which bindings in the function hold a
// telemetry payload -- payload-typed parameters, results, and receivers, locals
// constructed from a payload literal or new(...), copies of those bindings,
// results of package-level functions that return a payload (req := newRequest()),
// and package-scope payload variables declared in the same file -- so unrelated
// Attributes fields on other types are left alone while a payload handed to a
// helper is still checked. Bindings are tracked by parser object identity, not by
// name, so a shadowing loop or closure variable that reuses a payload's name is
// not mistaken for the payload. Payloads whose provenance cannot be seen
// syntactically (for example a method result, a cross-package call, an interface
// value, or a package-scope variable referenced from another file) are outside
// this best-effort guard; the primary gate remains the inline payload-literal
// scan.
func scanAttributeMutations(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	pkg *sourcePackage,
	receiver *ast.FieldList,
	signature *ast.FuncType,
	body *ast.BlockStmt,
) []string {
	if body == nil {
		return nil
	}

	payloadObjects := map[*parserObject]bool{}
	if pkg != nil {
		for object := range pkg.packagePayloadObjects {
			payloadObjects[object] = true
		}
	}
	addPayloadFieldObjects(receiver, source, payloadObjects)
	if signature != nil {
		addPayloadFieldObjects(signature.Params, source, payloadObjects)
		addPayloadFieldObjects(signature.Results, source, payloadObjects)
	}
	ast.Inspect(body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			addPayloadAssignmentObjects(value, source, pkg, payloadObjects)
		case *ast.ValueSpec:
			addPayloadValueSpecObjects(value, source, pkg, payloadObjects)
		case *ast.FuncLit:
			if value.Type != nil {
				addPayloadFieldObjects(value.Type.Params, source, payloadObjects)
				addPayloadFieldObjects(value.Type.Results, source, payloadObjects)
			}
		}
		return true
	})

	var diagnostics []string
	ast.Inspect(body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selector.Sel.Name != "Attributes" && selector.Sel.Name != "GetAttributes" {
			return true
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok || identifier.Obj == nil || !payloadObjects[identifier.Obj] {
			return true
		}
		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s:%d: set telemetry Attributes only inside the payload literal; "+
				"reading or assigning them after construction hides keys from governance",
			displayPath(extensionRoot, source.path),
			fset.Position(selector.Pos()).Line))
		return true
	})
	return diagnostics
}

// addPayloadFieldObjects records the binding identity of parameters, results, or
// receivers whose type is a telemetry payload (optionally a pointer to one).
func addPayloadFieldObjects(fields *ast.FieldList, source *sourceFile, objects map[*parserObject]bool) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		if !isPayloadTypeExpression(field.Type, source) {
			continue
		}
		for _, name := range field.Names {
			if name.Obj != nil {
				objects[name.Obj] = true
			}
		}
	}
}

// addPayloadAssignmentObjects records identifiers assigned a telemetry payload:
// a payload literal or new(...) (req := azdext.ReportUsageRequest{...}), the
// result of a package-level function that returns a payload (req := newRequest()),
// or a copy of a binding already known to be a payload (alias := req). Following
// the copy keeps a later alias.Attributes write from escaping the check.
func addPayloadAssignmentObjects(
	assignment *ast.AssignStmt,
	source *sourceFile,
	pkg *sourcePackage,
	objects map[*parserObject]bool,
) {
	for index, value := range assignment.Rhs {
		if index >= len(assignment.Lhs) {
			break
		}
		if !isPayloadExpression(value, source) &&
			!isKnownPayloadIdent(value, objects) &&
			!isPayloadReturningCall(value, pkg) {
			continue
		}
		if identifier, ok := assignment.Lhs[index].(*ast.Ident); ok && identifier.Obj != nil {
			objects[identifier.Obj] = true
		}
	}
}

// addPayloadValueSpecObjects records identifiers from var declarations that are
// typed as, initialized from, or a copy of a telemetry payload.
func addPayloadValueSpecObjects(
	spec *ast.ValueSpec,
	source *sourceFile,
	pkg *sourcePackage,
	objects map[*parserObject]bool,
) {
	if spec.Type != nil && isPayloadTypeExpression(spec.Type, source) {
		for _, name := range spec.Names {
			if name.Obj != nil {
				objects[name.Obj] = true
			}
		}
		return
	}
	for index, value := range spec.Values {
		if index >= len(spec.Names) {
			break
		}
		if !isPayloadExpression(value, source) &&
			!isKnownPayloadIdent(value, objects) &&
			!isPayloadReturningCall(value, pkg) {
			continue
		}
		if spec.Names[index].Obj != nil {
			objects[spec.Names[index].Obj] = true
		}
	}
}

// isKnownPayloadIdent reports whether an expression is an identifier already
// bound to a telemetry payload, so copies (alias := req) stay tracked.
func isKnownPayloadIdent(expression ast.Expr, objects map[*parserObject]bool) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && identifier.Obj != nil && objects[identifier.Obj]
}

// isPayloadReturningCall reports whether an expression is a call to a
// package-level function whose first result is a telemetry payload
// (req := newRequest()), so the local it initializes is tracked and a later
// req.Attributes write is still rejected. Only a bare function identifier is
// resolved: if it resolves to a non-function binding (a func-typed local that
// shadows the name) it is ignored, and method or cross-package calls stay
// outside this best-effort guard.
func isPayloadReturningCall(expression ast.Expr, pkg *sourcePackage) bool {
	if pkg == nil {
		return false
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	identifier, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	if identifier.Obj != nil && identifier.Obj.Kind != ast.Fun {
		return false
	}
	return pkg.payloadReturningFuncs[identifier.Name]
}

// isPayloadTypeExpression reports whether a type expression names a telemetry
// payload, unwrapping a single pointer (for example *azdext.ReportUsageRequest).
func isPayloadTypeExpression(expression ast.Expr, source *sourceFile) bool {
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	return isTelemetryPayloadType(expression, source)
}

// isPayloadExpression reports whether an expression evaluates to a telemetry
// payload whose provenance we can follow: a payload composite literal, a leading
// address-of (&Event{...}), or new(...) applied to a payload type
// (new(azdext.ReportUsageRequest)) or a payload literal (new(Event{})).
func isPayloadExpression(expression ast.Expr, source *sourceFile) bool {
	switch value := expression.(type) {
	case *ast.UnaryExpr:
		if value.Op == token.AND {
			return isPayloadExpression(value.X, source)
		}
	case *ast.CompositeLit:
		return isTelemetryPayloadType(value.Type, source)
	case *ast.CallExpr:
		identifier, ok := value.Fun.(*ast.Ident)
		if ok && identifier.Name == "new" && len(value.Args) == 1 {
			return isPayloadTypeExpression(value.Args[0], source) ||
				isPayloadExpression(value.Args[0], source)
		}
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
