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

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

const (
	azdextPackagePath           = "github.com/azure/azure-dev/cli/azd/pkg/azdext"
	azdextV1BetaPackagePath     = "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	foundryTelemetryPackagePath = "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	tracingFieldsPackagePath    = "github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	otelAttributePackagePath    = "go.opentelemetry.io/otel/attribute"
)

type telemetryUsage struct {
	extension string
	key       string
	path      string
	line      int
}

type sourceFile struct {
	path            string
	file            *ast.File
	imports         map[string]string
	dotImports      map[string]bool
	localImports    map[string]*sourcePackage
	localDotImports []*sourcePackage
	parents         map[ast.Node]ast.Node
}

type constDefinition struct {
	expression ast.Expr
	source     *sourceFile
}

type typeDefinition struct {
	expression ast.Expr
	source     *sourceFile
}

// parserObject preserves parser-local lexical identity without loading every nested extension module.
type parserObject = ast.Object //nolint:staticcheck // go/types would require loading extension dependencies.

type sourcePackage struct {
	directory             string
	importPath            string
	files                 []*sourceFile
	constants             map[string][]constDefinition
	objectConstants       map[*parserObject]constDefinition
	types                 map[string][]typeDefinition
	objectTypes           map[*parserObject]typeDefinition
	functionResults       map[string][][]bool
	objectFunctionResults map[*parserObject][]bool
	payloadObjects        map[*parserObject]bool
	payloadNames          map[string]bool
	attributeMapObjects   map[*parserObject]bool
	attributeMapNames     map[string]bool
	packageDeclarations   map[string]bool
	payloadTypeNames      map[string]bool
	telemetryEnabled      bool
}

// The scanner parses source only; it never executes extension code.
func scanExtensionTelemetry(extensionRoot string) ([]telemetryUsage, []string) {
	fset := token.NewFileSet()
	packages := map[string]*sourcePackage{}
	var diagnostics []string

	err := filepath.WalkDir(extensionRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
				"%s: failed to parse extension source: %v",
				filepath.ToSlash(path),
				parseErr,
			))
			return nil
		}

		source := &sourceFile{
			path:       path,
			file:       file,
			imports:    importAliases(file),
			dotImports: dotImports(file),
			parents:    parentNodes(file),
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
	if err != nil {
		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s: failed to scan extension source: %v",
			filepath.ToSlash(extensionRoot),
			err,
		))
	}

	var usages []telemetryUsage
	assignPackageImportPaths(extensionRoot, packages)
	telemetryPackages := findTelemetryPackages(extensionRoot, packages)
	linkLocalPackageImports(extensionRoot, packages)

	for _, pkg := range packages {
		pkg.telemetryEnabled = telemetryPackages[pkg]
		collectPackageDeclarations(pkg)
		collectConstants(pkg)
		collectTypeDefinitions(pkg)
	}
	collectPayloadTypeNames(packages)

	for _, pkg := range packages {
		usesTelemetryPayload := pkg.telemetryEnabled
		collectPayloadFunctionResults(pkg)
		collectPayloadObjects(pkg)
		collectAttributeMapObjects(pkg)

		for _, source := range pkg.files {
			ast.Inspect(source.file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.CompositeLit:
					isPayloadLiteral := isTelemetryPayloadType(value.Type, source, pkg) ||
						isTelemetryPayloadConversionLiteral(value, source, pkg)
					if isPayloadLiteral {
						payloadUsages, payloadDiagnostics := scanTelemetryPayload(
							fset,
							extensionRoot,
							source,
							pkg,
							value,
						)
						usages = append(usages, payloadUsages...)
						diagnostics = append(diagnostics, payloadDiagnostics...)
					} else if usesTelemetryPayload &&
						resolvesThroughGenericInstantiation(value.Type, source, pkg, nil) {
						position := fset.Position(value.Pos())
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: generic composite literals are not supported in extension "+
								"telemetry packages; use a concrete keyed telemetry payload literal",
							displayPath(extensionRoot, source.path),
							position.Line,
						))
					} else if usesTelemetryPayload &&
						compositeLiteralHasUnkeyedElements(value) &&
						resolvesToExternalNamedType(value.Type, source, pkg, nil) {
						position := fset.Position(value.Pos())
						diagnostics = append(diagnostics, fmt.Sprintf(
							"%s:%d: unresolved unkeyed composite literals are not supported in "+
								"extension telemetry packages; use a concrete keyed telemetry payload literal",
							displayPath(extensionRoot, source.path),
							position.Line,
						))
					}

					elidedUsages, elidedDiagnostics := scanTypeElidedPayloads(
						fset,
						extensionRoot,
						source,
						pkg,
						value,
					)
					usages = append(usages, elidedUsages...)
					diagnostics = append(diagnostics, elidedDiagnostics...)
				case *ast.AssignStmt:
					assignmentUsages, assignmentDiagnostics := scanTelemetryAttributeAssignments(
						fset,
						extensionRoot,
						source,
						pkg,
						value,
					)
					usages = append(usages, assignmentUsages...)
					diagnostics = append(diagnostics, assignmentDiagnostics...)
				case *ast.SelectorExpr:
					if isTelemetryPayloadSelector(value, source, pkg) {
						position := fset.Position(value.Pos())
						switch value.Sel.Name {
						case "Attributes":
							diagnostics = append(diagnostics, fmt.Sprintf(
								"%s:%d: extension telemetry Attributes must be declared inline in "+
									"the telemetry payload literal; post-construction access is not supported",
								displayPath(extensionRoot, source.path),
								position.Line,
							))
						case "GetAttributes":
							diagnostics = append(diagnostics, fmt.Sprintf(
								"%s:%d: extension telemetry GetAttributes access is not supported; "+
									"declare Attributes inline in the telemetry payload literal",
								displayPath(extensionRoot, source.path),
								position.Line,
							))
						}
					}
				}
				return true
			})
		}
	}

	return deduplicateTelemetryUsages(usages), deduplicateStrings(diagnostics)
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

func assignPackageImportPaths(extensionRoot string, packages map[string]*sourcePackage) {
	modulePaths := map[string]string{}
	for _, pkg := range packages {
		extension := extensionName(extensionRoot, pkg.files[0].path)
		modulePath, exists := modulePaths[extension]
		if !exists {
			modulePath = readModulePath(filepath.Join(extensionRoot, extension))
			modulePaths[extension] = modulePath
		}
		if modulePath == "" {
			continue
		}

		relative, err := filepath.Rel(filepath.Join(extensionRoot, extension), pkg.directory)
		if err != nil {
			continue
		}
		pkg.importPath = modulePath
		if relative != "." {
			pkg.importPath += "/" + filepath.ToSlash(relative)
		}
	}
}

func readModulePath(directory string) string {
	content, err := os.ReadFile(filepath.Join(directory, "go.mod"))
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(content)) {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[0] == "module" {
			return parts[1]
		}
	}
	return ""
}

func findTelemetryPackages(
	extensionRoot string,
	packages map[string]*sourcePackage,
) map[*sourcePackage]bool {
	result := map[*sourcePackage]bool{}
	for _, pkg := range packages {
		result[pkg] = packageReferencesTelemetryPayload(pkg)
	}

	for changed := true; changed; {
		changed = false
		for _, pkg := range packages {
			if result[pkg] || !packageImportsTelemetryPackage(extensionRoot, pkg, packages, result) {
				continue
			}
			result[pkg] = true
			changed = true
		}
	}
	return result
}

func packageImportsTelemetryPackage(
	extensionRoot string,
	pkg *sourcePackage,
	packages map[string]*sourcePackage,
	telemetryPackages map[*sourcePackage]bool,
) bool {
	for _, source := range pkg.files {
		for _, importPath := range source.imports {
			imported := localPackageForImport(extensionRoot, pkg, importPath, packages)
			if imported != nil && telemetryPackages[imported] {
				return true
			}
		}
		for importPath := range source.dotImports {
			imported := localPackageForImport(extensionRoot, pkg, importPath, packages)
			if imported != nil && telemetryPackages[imported] {
				return true
			}
		}
	}
	return false
}

func localPackageForImport(
	extensionRoot string,
	importer *sourcePackage,
	importPath string,
	packages map[string]*sourcePackage,
) *sourcePackage {
	extension := extensionName(extensionRoot, importer.files[0].path)
	extensionDir := filepath.Join(extensionRoot, extension)
	var result *sourcePackage

	for _, candidate := range packages {
		if candidate == importer ||
			extensionName(extensionRoot, candidate.files[0].path) != extension {
			continue
		}

		matches := candidate.importPath != "" && candidate.importPath == importPath
		if candidate.importPath == "" {
			relative, err := filepath.Rel(extensionDir, candidate.directory)
			if err != nil {
				continue
			}
			relativeImport := filepath.ToSlash(relative)
			if relativeImport == "." {
				matches = importPath == extension || strings.HasSuffix(importPath, "/"+extension)
			} else {
				matches = importPath == relativeImport || strings.HasSuffix(importPath, "/"+relativeImport)
			}
		}
		if !matches {
			continue
		}
		if result != nil {
			return nil
		}
		result = candidate
	}
	return result
}

func linkLocalPackageImports(
	extensionRoot string,
	packages map[string]*sourcePackage,
) {
	for _, pkg := range packages {
		for _, source := range pkg.files {
			source.localImports = map[string]*sourcePackage{}
			for alias, importPath := range source.imports {
				imported := localPackageForImport(extensionRoot, pkg, importPath, packages)
				if imported != nil {
					source.localImports[alias] = imported
				}
			}
			for importPath := range source.dotImports {
				imported := localPackageForImport(extensionRoot, pkg, importPath, packages)
				if imported != nil {
					source.localDotImports = append(source.localDotImports, imported)
				}
			}
		}
	}
}

func collectPayloadTypeNames(packages map[string]*sourcePackage) {
	for _, pkg := range packages {
		pkg.payloadTypeNames = map[string]bool{}
	}

	for changed := true; changed; {
		changed = false
		for _, pkg := range packages {
			for name, definitions := range pkg.types {
				if pkg.payloadTypeNames[name] {
					continue
				}
				isPayloadType := len(definitions) > 0
				for _, definition := range definitions {
					if !isTelemetryPayloadType(definition.expression, definition.source, pkg) {
						isPayloadType = false
						break
					}
				}
				if !isPayloadType {
					continue
				}
				pkg.payloadTypeNames[name] = true
				changed = true
			}
		}
	}
}

func parentNodes(file *ast.File) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	return parents
}

func packageReferencesTelemetryPayload(pkg *sourcePackage) bool {
	for _, source := range pkg.files {
		referencesPayload := false
		ast.Inspect(source.file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				referencesPayload = isImportedTelemetryPayloadSelector(value, source)
			case *ast.Ident:
				referencesPayload = isDotImportedTelemetryPayloadIdentifier(value, source)
			}
			return !referencesPayload
		})
		if referencesPayload {
			return true
		}
	}
	return false
}

func compositeLiteralHasUnkeyedElements(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		if _, ok := element.(*ast.KeyValueExpr); !ok {
			return true
		}
	}
	return false
}

func compositeLiteralContainsUnkeyedTypeElidedPayloadShape(
	literal *ast.CompositeLit,
	source *sourceFile,
	pkg *sourcePackage,
) bool {
	for _, element := range literal.Elts {
		expressions := []ast.Expr{element}
		if keyValue, ok := element.(*ast.KeyValueExpr); ok {
			expressions = []ast.Expr{keyValue.Key, keyValue.Value}
		}
		for _, expression := range expressions {
			nested := payloadCompositeLiteral(expression)
			if nested == nil || nested.Type != nil {
				continue
			}
			if len(nested.Elts) == 2 &&
				compositeLiteralHasUnkeyedElements(nested) &&
				!isClearlyScalarExpression(nested.Elts[1], source, pkg) {
				return true
			}
			if compositeLiteralContainsUnkeyedTypeElidedPayloadShape(nested, source, pkg) {
				return true
			}
		}
	}
	return false
}

func isClearlyScalarExpression(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
) bool {
	if _, ok := resolveStringConstant(expression, source, pkg, nil); ok {
		return true
	}
	if _, ok := resolveBoolConstant(expression, source, pkg, nil); ok {
		return true
	}

	switch value := expression.(type) {
	case *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return isClearlyScalarExpression(value.X, source, pkg)
	case *ast.UnaryExpr:
		return isClearlyScalarExpression(value.X, source, pkg)
	case *ast.BinaryExpr:
		return isClearlyScalarExpression(value.X, source, pkg) &&
			isClearlyScalarExpression(value.Y, source, pkg)
	case *ast.Ident:
		if value.Name == "nil" && value.Obj == nil && !pkg.packageDeclarations["nil"] {
			return true
		}
		if value.Obj != nil {
			return value.Obj.Kind == ast.Con
		}
		return len(pkg.constants[value.Name]) > 0
	default:
		return false
	}
}

func resolvesToExternalNamedType(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
	resolving *typeResolution,
) bool {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		alias, ok := value.X.(*ast.Ident)
		if !ok || alias.Obj != nil && alias.Obj.Kind != ast.Pkg {
			return false
		}
		importPath, ok := source.imports[alias.Name]
		if !ok {
			return pkg.telemetryEnabled
		}
		return importPath != "" && !isTelemetryPayloadPackagePath(importPath)
	case *ast.ParenExpr:
		return resolvesToExternalNamedType(value.X, source, pkg, resolving)
	case *ast.StarExpr:
		return resolvesToExternalNamedType(value.X, source, pkg, resolving)
	case *ast.IndexExpr:
		return resolvesToExternalNamedType(value.X, source, pkg, resolving)
	case *ast.IndexListExpr:
		return resolvesToExternalNamedType(value.X, source, pkg, resolving)
	case *ast.Ident:
		if value.Obj != nil {
			if value.Obj.Kind != ast.Typ {
				return false
			}
			definition, ok := pkg.objectTypes[value.Obj]
			if !ok {
				return false
			}
			resolving = ensureTypeResolution(resolving)
			if resolving.objects[value.Obj] {
				return false
			}
			resolving.objects[value.Obj] = true
			defer delete(resolving.objects, value.Obj)
			return resolvesToExternalNamedType(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			)
		}
		definitions := pkg.types[value.Name]
		if len(definitions) == 0 {
			return pkg.telemetryEnabled && sourceHasExternalDotImport(source)
		}
		resolving = ensureTypeResolution(resolving)
		if resolving.names[value.Name] {
			return false
		}
		resolving.names[value.Name] = true
		defer delete(resolving.names, value.Name)
		for _, definition := range definitions {
			if resolvesToExternalNamedType(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func sourceHasExternalDotImport(source *sourceFile) bool {
	for importPath := range source.dotImports {
		if !isTelemetryPayloadPackagePath(importPath) {
			return true
		}
	}
	return false
}

func resolvesThroughGenericInstantiation(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
	resolving *typeResolution,
) bool {
	switch value := expression.(type) {
	case *ast.IndexExpr, *ast.IndexListExpr:
		return true
	case *ast.ParenExpr:
		return resolvesThroughGenericInstantiation(value.X, source, pkg, resolving)
	case *ast.StarExpr:
		return resolvesThroughGenericInstantiation(value.X, source, pkg, resolving)
	case *ast.ArrayType:
		return resolvesThroughGenericInstantiation(value.Elt, source, pkg, resolving)
	case *ast.MapType:
		return resolvesThroughGenericInstantiation(value.Key, source, pkg, resolving) ||
			resolvesThroughGenericInstantiation(value.Value, source, pkg, resolving)
	case *ast.StructType:
		if value.Fields == nil {
			return false
		}
		for _, field := range value.Fields.List {
			if resolvesThroughGenericInstantiation(field.Type, source, pkg, resolving) {
				return true
			}
		}
		return false
	case *ast.Ident:
		if value.Obj != nil {
			if value.Obj.Kind != ast.Typ {
				return false
			}
			definition, ok := pkg.objectTypes[value.Obj]
			if !ok {
				return false
			}
			resolving = ensureTypeResolution(resolving)
			if resolving.objects[value.Obj] {
				return false
			}
			resolving.objects[value.Obj] = true
			defer delete(resolving.objects, value.Obj)
			return resolvesThroughGenericInstantiation(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			)
		}
		definitions := pkg.types[value.Name]
		if len(definitions) == 0 {
			return false
		}
		resolving = ensureTypeResolution(resolving)
		if resolving.names[value.Name] {
			return false
		}
		resolving.names[value.Name] = true
		defer delete(resolving.names, value.Name)
		for _, definition := range definitions {
			if resolvesThroughGenericInstantiation(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func scanTelemetryPayload(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	pkg *sourcePackage,
	literal *ast.CompositeLit,
) ([]telemetryUsage, []string) {
	var attributes ast.Expr
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			position := fset.Position(element.Pos())
			return nil, []string{fmt.Sprintf(
				"%s:%d: extension telemetry payload literals must use keyed fields",
				displayPath(extensionRoot, source.path),
				position.Line,
			)}
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if ok && key.Name == "Attributes" {
			attributes = keyValue.Value
			break
		}
	}

	if attributes == nil {
		return nil, nil
	}
	return scanTelemetryAttributes(fset, extensionRoot, source, pkg, attributes)
}

func scanTypeElidedPayloads(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	pkg *sourcePackage,
	container *ast.CompositeLit,
) ([]telemetryUsage, []string) {
	return scanTypeElidedElements(
		fset,
		extensionRoot,
		source,
		source,
		pkg,
		container.Type,
		container,
	)
}

func scanTypeElidedElements(
	fset *token.FileSet,
	extensionRoot string,
	literalSource *sourceFile,
	typeSource *sourceFile,
	pkg *sourcePackage,
	containerType ast.Expr,
	container *ast.CompositeLit,
) ([]telemetryUsage, []string) {
	containerTypes, ok := resolveTelemetryContainerTypes(containerType, typeSource, pkg, nil)
	if !ok {
		if containerType != nil &&
			pkg.telemetryEnabled &&
			compositeLiteralContainsUnkeyedTypeElidedPayloadShape(container, literalSource, pkg) {
			position := fset.Position(container.Pos())
			return nil, []string{fmt.Sprintf(
				"%s:%d: unresolved unkeyed composite literals are not supported in "+
					"extension telemetry packages; use a concrete keyed telemetry payload literal",
				displayPath(extensionRoot, literalSource.path),
				position.Line,
			)}
		}
		return nil, nil
	}

	var usages []telemetryUsage
	var diagnostics []string
	for _, element := range container.Elts {
		if containerTypes.key != nil {
			keyValue, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			keyUsages, keyDiagnostics := scanContextualTelemetryLiteral(
				fset,
				extensionRoot,
				literalSource,
				containerTypes.source,
				pkg,
				containerTypes.key,
				keyValue.Key,
			)
			usages = append(usages, keyUsages...)
			diagnostics = append(diagnostics, keyDiagnostics...)

			valueUsages, valueDiagnostics := scanContextualTelemetryLiteral(
				fset,
				extensionRoot,
				literalSource,
				containerTypes.source,
				pkg,
				containerTypes.value,
				keyValue.Value,
			)
			usages = append(usages, valueUsages...)
			diagnostics = append(diagnostics, valueDiagnostics...)
			continue
		}

		expression := ast.Expr(element)
		if keyValue, ok := element.(*ast.KeyValueExpr); ok {
			expression = keyValue.Value
		}
		elementUsages, elementDiagnostics := scanContextualTelemetryLiteral(
			fset,
			extensionRoot,
			literalSource,
			containerTypes.source,
			pkg,
			containerTypes.value,
			expression,
		)
		usages = append(usages, elementUsages...)
		diagnostics = append(diagnostics, elementDiagnostics...)
	}
	return usages, diagnostics
}

type telemetryContainerTypes struct {
	key    ast.Expr
	value  ast.Expr
	source *sourceFile
}

func scanContextualTelemetryLiteral(
	fset *token.FileSet,
	extensionRoot string,
	literalSource *sourceFile,
	typeSource *sourceFile,
	pkg *sourcePackage,
	expectedType ast.Expr,
	expression ast.Expr,
) ([]telemetryUsage, []string) {
	literal := payloadCompositeLiteral(expression)
	if literal == nil || literal.Type != nil {
		return nil, nil
	}
	if isTelemetryPayloadType(expectedType, typeSource, pkg) {
		return scanTelemetryPayload(fset, extensionRoot, literalSource, pkg, literal)
	}
	if pkg.telemetryEnabled &&
		compositeLiteralHasUnkeyedElements(literal) &&
		resolvesToExternalNamedType(expectedType, typeSource, pkg, nil) {
		position := fset.Position(literal.Pos())
		return nil, []string{fmt.Sprintf(
			"%s:%d: unresolved unkeyed composite literals are not supported in "+
				"extension telemetry packages; use a concrete keyed telemetry payload literal",
			displayPath(extensionRoot, literalSource.path),
			position.Line,
		)}
	}
	return scanTypeElidedElements(
		fset,
		extensionRoot,
		literalSource,
		typeSource,
		pkg,
		expectedType,
		literal,
	)
}

func scanTelemetryAttributeAssignments(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	pkg *sourcePackage,
	assignment *ast.AssignStmt,
) ([]telemetryUsage, []string) {
	var usages []telemetryUsage
	var diagnostics []string

	for i, left := range assignment.Lhs {
		left = unwrapParentheses(left)
		right := ast.Expr(nil)
		if i < len(assignment.Rhs) {
			right = assignment.Rhs[i]
		}

		if selector, ok := left.(*ast.SelectorExpr); ok &&
			isTelemetryAttributesSelector(selector, source, pkg) {
			if right == nil {
				position := fset.Position(left.Pos())
				diagnostics = append(diagnostics, fmt.Sprintf(
					"%s:%d: extension telemetry Attributes assignment must have a directly analyzable value",
					displayPath(extensionRoot, source.path),
					position.Line,
				))
				continue
			}
			assignmentUsages, assignmentDiagnostics := scanTelemetryAttributes(
				fset,
				extensionRoot,
				source,
				pkg,
				right,
			)
			usages = append(usages, assignmentUsages...)
			diagnostics = append(diagnostics, assignmentDiagnostics...)
			continue
		}

		index, ok := left.(*ast.IndexExpr)
		if !ok {
			continue
		}
		if !isTelemetryAttributeMapExpression(index.X, source, pkg) {
			continue
		}
		usage, diagnostic := scanTelemetryAttributeKey(
			fset,
			extensionRoot,
			source,
			pkg,
			index.Index,
		)
		if diagnostic != "" {
			diagnostics = append(diagnostics, diagnostic)
		} else {
			usages = append(usages, usage)
		}
	}

	return usages, diagnostics
}

func unwrapParentheses(expression ast.Expr) ast.Expr {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			return expression
		}
		expression = parenthesized.X
	}
}

func scanTelemetryAttributes(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	pkg *sourcePackage,
	attributes ast.Expr,
) ([]telemetryUsage, []string) {
	if ident, ok := attributes.(*ast.Ident); ok &&
		ident.Name == "nil" &&
		ident.Obj == nil &&
		!pkg.packageDeclarations["nil"] {
		return nil, nil
	}

	attributesLiteral, ok := attributes.(*ast.CompositeLit)
	if !ok || !isStringMapType(attributesLiteral.Type) {
		position := fset.Position(attributes.Pos())
		return nil, []string{fmt.Sprintf(
			"%s:%d: extension telemetry Attributes must be an inline map[string]string literal or nil",
			displayPath(extensionRoot, source.path),
			position.Line,
		)}
	}

	var usages []telemetryUsage
	var diagnostics []string
	for _, element := range attributesLiteral.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		usage, diagnostic := scanTelemetryAttributeKey(
			fset,
			extensionRoot,
			source,
			pkg,
			keyValue.Key,
		)
		if diagnostic != "" {
			diagnostics = append(diagnostics, diagnostic)
		} else {
			usages = append(usages, usage)
		}
	}
	return usages, diagnostics
}

func scanTelemetryAttributeKey(
	fset *token.FileSet,
	extensionRoot string,
	source *sourceFile,
	pkg *sourcePackage,
	expression ast.Expr,
) (telemetryUsage, string) {
	relativePath := displayPath(extensionRoot, source.path)
	extension := strings.Split(relativePath, "/")[0]
	position := fset.Position(expression.Pos())
	key, ok := resolveStringConstant(expression, source, pkg, nil)
	if !ok {
		return telemetryUsage{}, fmt.Sprintf(
			"%s:%d: %s extension telemetry attribute key must be a string literal or "+
				"same-package compile-time string constant",
			relativePath,
			position.Line,
			extension,
		)
	}
	if key == "" {
		return telemetryUsage{}, fmt.Sprintf(
			"%s:%d: %s extension telemetry attribute key must not be empty",
			relativePath,
			position.Line,
			extension,
		)
	}

	return telemetryUsage{
		extension: extension,
		key:       key,
		path:      relativePath,
		line:      position.Line,
	}, ""
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

func collectTypeDefinitions(pkg *sourcePackage) {
	pkg.types = map[string][]typeDefinition{}
	pkg.objectTypes = map[*parserObject]typeDefinition{}

	for _, source := range pkg.files {
		for _, declaration := range source.file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			collectTypeDeclaration(pkg, source, gen, true)
		}
		ast.Inspect(source.file, func(node ast.Node) bool {
			gen, ok := node.(*ast.GenDecl)
			if ok && gen.Tok == token.TYPE {
				collectTypeDeclaration(pkg, source, gen, false)
			}
			return true
		})
	}
}

func collectTypeDeclaration(
	pkg *sourcePackage,
	source *sourceFile,
	gen *ast.GenDecl,
	packageScope bool,
) {
	for _, spec := range gen.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		definition := typeDefinition{
			expression: typeSpec.Type,
			source:     source,
		}
		if typeSpec.Name.Obj != nil {
			pkg.objectTypes[typeSpec.Name.Obj] = definition
		}
		if packageScope {
			pkg.types[typeSpec.Name.Name] = append(pkg.types[typeSpec.Name.Name], definition)
		}
	}
}

func collectPayloadFunctionResults(pkg *sourcePackage) {
	pkg.functionResults = map[string][][]bool{}
	pkg.objectFunctionResults = map[*parserObject][]bool{}

	for _, source := range pkg.files {
		for _, declaration := range source.file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil {
				continue
			}
			results := telemetryFunctionTypeResults(function.Type, source, pkg)
			recordPayloadFunctionResults(pkg, function.Name, results, true)
		}
	}

	for changed := true; changed; {
		changed = false
		for _, source := range pkg.files {
			for _, declaration := range source.file.Decls {
				gen, ok := declaration.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if ok {
						changed = collectFunctionValueSpec(
							pkg,
							source,
							valueSpec,
							true,
						) || changed
					}
				}
			}

			ast.Inspect(source.file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.ValueSpec:
					changed = collectFunctionValueSpec(
						pkg,
						source,
						value,
						false,
					) || changed
				case *ast.AssignStmt:
					if len(value.Lhs) != len(value.Rhs) {
						return true
					}
					for i, left := range value.Lhs {
						name, ok := left.(*ast.Ident)
						if !ok {
							continue
						}
						results := telemetryFunctionExpressionResults(
							value.Rhs[i],
							source,
							pkg,
						)
						changed = recordPayloadFunctionResults(
							pkg,
							name,
							results,
							false,
						) || changed
					}
				}
				return true
			})
		}
	}
}

func collectFunctionValueSpec(
	pkg *sourcePackage,
	source *sourceFile,
	valueSpec *ast.ValueSpec,
	packageScope bool,
) bool {
	changed := false
	if functionType, ok := valueSpec.Type.(*ast.FuncType); ok {
		results := telemetryFunctionTypeResults(functionType, source, pkg)
		for _, name := range valueSpec.Names {
			changed = recordPayloadFunctionResults(
				pkg,
				name,
				results,
				packageScope,
			) || changed
		}
	}
	if len(valueSpec.Names) != len(valueSpec.Values) {
		return changed
	}
	for i, name := range valueSpec.Names {
		results := telemetryFunctionExpressionResults(valueSpec.Values[i], source, pkg)
		changed = recordPayloadFunctionResults(
			pkg,
			name,
			results,
			packageScope,
		) || changed
	}
	return changed
}

func recordPayloadFunctionResults(
	pkg *sourcePackage,
	name *ast.Ident,
	results []bool,
	packageScope bool,
) bool {
	if name == nil || !slicesContainTrue(results) {
		return false
	}
	changed := false
	if name.Obj != nil {
		if _, exists := pkg.objectFunctionResults[name.Obj]; !exists {
			pkg.objectFunctionResults[name.Obj] = results
			changed = true
		}
	}
	if packageScope && !containsBoolSlice(pkg.functionResults[name.Name], results) {
		pkg.functionResults[name.Name] = append(pkg.functionResults[name.Name], results)
		changed = true
	}
	return changed
}

func telemetryFunctionExpressionResults(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
) []bool {
	switch value := expression.(type) {
	case *ast.FuncLit:
		return telemetryFunctionTypeResults(value.Type, source, pkg)
	case *ast.ParenExpr:
		return telemetryFunctionExpressionResults(value.X, source, pkg)
	case *ast.Ident:
		if value.Obj != nil {
			return pkg.objectFunctionResults[value.Obj]
		}
		definitions := pkg.functionResults[value.Name]
		if len(definitions) == 0 {
			return nil
		}
		results := definitions[0]
		for _, current := range definitions[1:] {
			if !boolSlicesEqual(results, current) {
				return nil
			}
		}
		return results
	default:
		return nil
	}
}

func telemetryFunctionTypeResults(
	functionType *ast.FuncType,
	source *sourceFile,
	pkg *sourcePackage,
) []bool {
	if functionType.Results == nil {
		return nil
	}
	var results []bool
	for _, field := range functionType.Results.List {
		count := max(1, len(field.Names))
		isPayload := isTelemetryPayloadType(field.Type, source, pkg)
		for range count {
			results = append(results, isPayload)
		}
	}
	return results
}

func collectPayloadObjects(pkg *sourcePackage) {
	pkg.payloadObjects = map[*parserObject]bool{}
	pkg.payloadNames = map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, source := range pkg.files {
			for _, declaration := range source.file.Decls {
				gen, ok := declaration.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					if valueSpec.Type != nil &&
						isTelemetryPayloadType(valueSpec.Type, source, pkg) {
						changed = markPayloadIdentifiers(
							valueSpec.Names,
							nil,
							pkg,
							true,
						) || changed
						continue
					}
					results := telemetryPayloadAssignmentResults(
						valueSpec.Values,
						len(valueSpec.Names),
						source,
						pkg,
					)
					changed = markPayloadIdentifiers(
						valueSpec.Names,
						results,
						pkg,
						true,
					) || changed
				}
			}

			ast.Inspect(source.file, func(node ast.Node) bool {
				switch value := node.(type) {
				case *ast.ValueSpec:
					if value.Type != nil && isTelemetryPayloadType(value.Type, source, pkg) {
						changed = markPayloadIdentifiers(value.Names, nil, pkg, false) || changed
						break
					}
					results := telemetryPayloadAssignmentResults(
						value.Values,
						len(value.Names),
						source,
						pkg,
					)
					changed = markPayloadIdentifiers(value.Names, results, pkg, false) || changed
				case *ast.Field:
					if !isTelemetryPayloadType(value.Type, source, pkg) {
						break
					}
					changed = markPayloadIdentifiers(value.Names, nil, pkg, false) || changed
				case *ast.AssignStmt:
					names := make([]*ast.Ident, len(value.Lhs))
					for i, left := range value.Lhs {
						names[i], _ = left.(*ast.Ident)
					}
					results := telemetryPayloadAssignmentResults(
						value.Rhs,
						len(value.Lhs),
						source,
						pkg,
					)
					changed = markPayloadIdentifiers(names, results, pkg, false) || changed
				}
				return true
			})
		}
	}
}

func markPayloadIdentifiers(
	names []*ast.Ident,
	results []bool,
	pkg *sourcePackage,
	packageScope bool,
) bool {
	changed := false
	for i, name := range names {
		isPayload := results == nil || i < len(results) && results[i]
		if !isPayload || name == nil {
			continue
		}
		if name.Obj != nil && !pkg.payloadObjects[name.Obj] {
			pkg.payloadObjects[name.Obj] = true
			changed = true
		}
		if packageScope && !pkg.payloadNames[name.Name] {
			pkg.payloadNames[name.Name] = true
			changed = true
		}
	}
	return changed
}

func telemetryPayloadAssignmentResults(
	expressions []ast.Expr,
	targetCount int,
	source *sourceFile,
	pkg *sourcePackage,
) []bool {
	results := make([]bool, targetCount)
	if len(expressions) == targetCount {
		for i, expression := range expressions {
			results[i] = isTelemetryPayloadExpression(expression, source, pkg)
		}
		return results
	}
	if len(expressions) != 1 {
		return results
	}

	call, ok := expressions[0].(*ast.CallExpr)
	if !ok {
		return results
	}
	callResults := telemetryPayloadCallResults(call, source, pkg)
	if len(callResults) == targetCount {
		copy(results, callResults)
	}
	return results
}

func isTelemetryPayloadExpression(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
) bool {
	switch value := expression.(type) {
	case *ast.CompositeLit:
		return isTelemetryPayloadType(value.Type, source, pkg)
	case *ast.ParenExpr:
		return isTelemetryPayloadExpression(value.X, source, pkg)
	case *ast.UnaryExpr:
		return value.Op == token.AND && isTelemetryPayloadExpression(value.X, source, pkg)
	case *ast.CallExpr:
		results := telemetryPayloadCallResults(value, source, pkg)
		return len(results) == 1 && results[0]
	case *ast.Ident:
		if value.Obj != nil {
			return pkg.payloadObjects[value.Obj]
		}
		return pkg.payloadNames[value.Name]
	default:
		return false
	}
}

func telemetryPayloadCallResults(
	call *ast.CallExpr,
	source *sourceFile,
	pkg *sourcePackage,
) []bool {
	if name, ok := unwrapParentheses(call.Fun).(*ast.Ident); ok &&
		name.Name == "new" &&
		name.Obj == nil &&
		len(call.Args) == 1 {
		return []bool{isTelemetryPayloadType(call.Args[0], source, pkg)}
	}
	return telemetryFunctionExpressionResults(call.Fun, source, pkg)
}

func slicesContainTrue(values []bool) bool {
	for _, value := range values {
		if value {
			return true
		}
	}
	return false
}

func boolSlicesEqual(left, right []bool) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func containsBoolSlice(values [][]bool, candidate []bool) bool {
	for _, value := range values {
		if boolSlicesEqual(value, candidate) {
			return true
		}
	}
	return false
}

func collectAttributeMapObjects(pkg *sourcePackage) {
	pkg.attributeMapObjects = map[*parserObject]bool{}
	pkg.attributeMapNames = map[string]bool{}
	for changed := true; changed; {
		changed = false
		for _, source := range pkg.files {
			for _, declaration := range source.file.Decls {
				gen, ok := declaration.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					valueSpec, ok := spec.(*ast.ValueSpec)
					if !ok || len(valueSpec.Names) != len(valueSpec.Values) {
						continue
					}
					for i, name := range valueSpec.Names {
						if !isTelemetryAttributeMapExpression(valueSpec.Values[i], source, pkg) {
							continue
						}
						changed = markAttributeMapIdentifier(
							name,
							pkg,
							true,
						) || changed
					}
				}
			}

			ast.Inspect(source.file, func(node ast.Node) bool {
				var names []*ast.Ident
				var expressions []ast.Expr
				switch value := node.(type) {
				case *ast.ValueSpec:
					names = value.Names
					expressions = value.Values
				case *ast.AssignStmt:
					if len(value.Lhs) != len(value.Rhs) {
						return true
					}
					names = make([]*ast.Ident, len(value.Lhs))
					for i, left := range value.Lhs {
						names[i], _ = left.(*ast.Ident)
					}
					expressions = value.Rhs
				default:
					return true
				}

				if len(names) != len(expressions) {
					return true
				}
				for i, name := range names {
					if !isTelemetryAttributeMapExpression(expressions[i], source, pkg) {
						continue
					}
					changed = markAttributeMapIdentifier(name, pkg, false) || changed
				}
				return true
			})
		}
	}
}

func markAttributeMapIdentifier(
	name *ast.Ident,
	pkg *sourcePackage,
	packageScope bool,
) bool {
	if name == nil {
		return false
	}
	changed := false
	if name.Obj != nil && !pkg.attributeMapObjects[name.Obj] {
		pkg.attributeMapObjects[name.Obj] = true
		changed = true
	}
	if packageScope && !pkg.attributeMapNames[name.Name] {
		pkg.attributeMapNames[name.Name] = true
		changed = true
	}
	return changed
}

func isTelemetryAttributeMapExpression(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
) bool {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		return isTelemetryAttributesSelector(value, source, pkg)
	case *ast.ParenExpr:
		return isTelemetryAttributeMapExpression(value.X, source, pkg)
	case *ast.Ident:
		if value.Obj != nil {
			return pkg.attributeMapObjects[value.Obj]
		}
		return pkg.attributeMapNames[value.Name]
	default:
		return false
	}
}

func payloadCompositeLiteral(expression ast.Expr) *ast.CompositeLit {
	switch value := expression.(type) {
	case *ast.CompositeLit:
		return value
	case *ast.ParenExpr:
		return payloadCompositeLiteral(value.X)
	case *ast.UnaryExpr:
		if value.Op == token.AND {
			return payloadCompositeLiteral(value.X)
		}
	}
	return nil
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

func isTelemetryPayloadType(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
) bool {
	return isTelemetryPayloadTypeResolving(expression, source, pkg, nil)
}

type typeResolution struct {
	objects map[*parserObject]bool
	names   map[string]bool
}

func isTelemetryPayloadTypeResolving(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
	resolving *typeResolution,
) bool {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		if isImportedTelemetryPayloadSelector(value, source) {
			return true
		}
		alias, ok := value.X.(*ast.Ident)
		if !ok || alias.Obj != nil && alias.Obj.Kind != ast.Pkg {
			return false
		}
		imported := source.localImports[alias.Name]
		return imported != nil && imported.payloadTypeNames[value.Sel.Name]
	case *ast.Ident:
		if value.Obj != nil {
			if value.Obj.Kind != ast.Typ {
				return false
			}
			definition, ok := pkg.objectTypes[value.Obj]
			if !ok {
				return false
			}
			resolving = ensureTypeResolution(resolving)
			if resolving.objects[value.Obj] {
				return false
			}
			resolving.objects[value.Obj] = true
			defer delete(resolving.objects, value.Obj)
			return isTelemetryPayloadTypeResolving(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			)
		}
		if isDotImportedTelemetryPayloadIdentifier(value, source) {
			return true
		}
		for _, imported := range source.localDotImports {
			if imported.payloadTypeNames[value.Name] {
				return true
			}
		}
		definitions := pkg.types[value.Name]
		if len(definitions) == 0 {
			return false
		}
		resolving = ensureTypeResolution(resolving)
		if resolving.names[value.Name] {
			return false
		}
		resolving.names[value.Name] = true
		defer delete(resolving.names, value.Name)
		for _, definition := range definitions {
			if !isTelemetryPayloadTypeResolving(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			) {
				return false
			}
		}
		return true
	case *ast.ParenExpr:
		return isTelemetryPayloadTypeResolving(value.X, source, pkg, resolving)
	case *ast.StarExpr:
		return isTelemetryPayloadTypeResolving(value.X, source, pkg, resolving)
	case *ast.IndexExpr:
		return isTelemetryPayloadTypeResolving(value.X, source, pkg, resolving)
	case *ast.IndexListExpr:
		return isTelemetryPayloadTypeResolving(value.X, source, pkg, resolving)
	default:
		return false
	}
}

func isTelemetryPayloadConversionLiteral(
	literal *ast.CompositeLit,
	source *sourceFile,
	pkg *sourcePackage,
) bool {
	var expression ast.Node = literal
	for {
		switch parent := source.parents[expression].(type) {
		case *ast.ParenExpr:
			expression = parent
		case *ast.UnaryExpr:
			if parent.Op != token.AND || parent.X != expression {
				return false
			}
			expression = parent
		case *ast.CallExpr:
			return len(parent.Args) == 1 &&
				parent.Args[0] == expression &&
				isTelemetryPayloadType(parent.Fun, source, pkg)
		default:
			return false
		}
	}
}

func resolveTelemetryContainerTypes(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
	resolving *typeResolution,
) (telemetryContainerTypes, bool) {
	switch value := expression.(type) {
	case *ast.ArrayType:
		return telemetryContainerTypes{value: value.Elt, source: source}, true
	case *ast.MapType:
		return telemetryContainerTypes{
			key:    value.Key,
			value:  value.Value,
			source: source,
		}, true
	case *ast.ParenExpr:
		return resolveTelemetryContainerTypes(value.X, source, pkg, resolving)
	case *ast.StarExpr:
		return resolveTelemetryContainerTypes(value.X, source, pkg, resolving)
	case *ast.IndexExpr:
		return resolveTelemetryContainerTypes(value.X, source, pkg, resolving)
	case *ast.IndexListExpr:
		return resolveTelemetryContainerTypes(value.X, source, pkg, resolving)
	case *ast.Ident:
		if value.Obj != nil {
			if value.Obj.Kind != ast.Typ {
				return telemetryContainerTypes{}, false
			}
			definition, ok := pkg.objectTypes[value.Obj]
			if !ok {
				return telemetryContainerTypes{}, false
			}
			resolving = ensureTypeResolution(resolving)
			if resolving.objects[value.Obj] {
				return telemetryContainerTypes{}, false
			}
			resolving.objects[value.Obj] = true
			defer delete(resolving.objects, value.Obj)
			return resolveTelemetryContainerTypes(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			)
		}

		definitions := pkg.types[value.Name]
		if len(definitions) == 0 {
			return telemetryContainerTypes{}, false
		}
		resolving = ensureTypeResolution(resolving)
		if resolving.names[value.Name] {
			return telemetryContainerTypes{}, false
		}
		resolving.names[value.Name] = true
		defer delete(resolving.names, value.Name)
		for _, definition := range definitions {
			containerTypes, ok := resolveTelemetryContainerTypes(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			)
			if ok {
				return containerTypes, true
			}
		}
		return telemetryContainerTypes{}, false
	default:
		return telemetryContainerTypes{}, false
	}
}

func ensureTypeResolution(resolving *typeResolution) *typeResolution {
	if resolving != nil {
		return resolving
	}
	return &typeResolution{
		objects: map[*parserObject]bool{},
		names:   map[string]bool{},
	}
}

func isTelemetryAttributesSelector(
	selector *ast.SelectorExpr,
	source *sourceFile,
	pkg *sourcePackage,
) bool {
	return selector.Sel.Name == "Attributes" &&
		isTelemetryPayloadExpression(selector.X, source, pkg)
}

func isTelemetryPayloadSelector(
	selector *ast.SelectorExpr,
	source *sourceFile,
	pkg *sourcePackage,
) bool {
	switch selector.Sel.Name {
	case "Attributes", "GetAttributes":
		return isTelemetryPayloadExpression(selector.X, source, pkg)
	default:
		return false
	}
}

func isReportUsagePackagePath(path string) bool {
	return path == azdextPackagePath || path == azdextV1BetaPackagePath
}

func isTelemetryPayloadPackagePath(path string) bool {
	return isReportUsagePackagePath(path) || path == foundryTelemetryPackagePath
}

func isImportedTelemetryPayloadSelector(selector *ast.SelectorExpr, source *sourceFile) bool {
	alias, ok := selector.X.(*ast.Ident)
	if !ok || alias.Obj != nil && alias.Obj.Kind != ast.Pkg {
		return false
	}
	importPath := source.imports[alias.Name]
	return isReportUsagePackagePath(importPath) && selector.Sel.Name == "ReportUsageRequest" ||
		importPath == foundryTelemetryPackagePath && selector.Sel.Name == "Event"
}

func isDotImportedTelemetryPayloadIdentifier(identifier *ast.Ident, source *sourceFile) bool {
	if identifier.Obj != nil {
		return false
	}
	return identifier.Name == "ReportUsageRequest" &&
		(source.dotImports[azdextPackagePath] || source.dotImports[azdextV1BetaPackagePath]) ||
		identifier.Name == "Event" && source.dotImports[foundryTelemetryPackagePath]
}

func isStringMapType(expression ast.Expr) bool {
	mapType, ok := expression.(*ast.MapType)
	if !ok {
		return false
	}
	key, keyOK := mapType.Key.(*ast.Ident)
	value, valueOK := mapType.Value.(*ast.Ident)
	return keyOK && valueOK && key.Name == "string" && value.Name == "string"
}

func displayPath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}
