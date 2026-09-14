// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const (
	eventsSource = "cli/azd/internal/tracing/events/events.go"
	fieldsSource = "cli/azd/internal/tracing/fields/fields.go"
	schemaDoc    = "docs/specs/metrics-audit/telemetry-schema.md"
	referenceDoc = "docs/reference/telemetry-data.md"
)

type definition struct {
	kind   string
	value  string
	source string
	line   int
}

type document struct {
	path   string
	values map[string]struct{}
}

type issue struct {
	def definition
	doc string
}

type extensionUsage struct {
	root        string
	definitions []definition
}

type attributeMapReferenceKey struct {
	path string
	pos  token.Pos
}

type attributeScope struct {
	bindings map[string][]definition
}

type extensionStaticData struct {
	constants                  map[string]string
	attributeMaps              map[string][]definition
	attributeMapReferences     map[attributeMapReferenceKey][]definition
	attributeMapReferenceKnown map[attributeMapReferenceKey]bool
}

type extensionSourceFile struct {
	root    string
	path    string
	file    *ast.File
	fileSet *token.FileSet
	data    *extensionStaticData
}

func main() {
	repoRootFlag := flag.String(
		"repo-root",
		"",
		"repository root (defaults to the current repository)",
	)
	flag.Parse()

	repoRoot := *repoRootFlag
	var err error
	if repoRoot == "" {
		repoRoot, err = findRepoRoot(".")
	} else {
		repoRoot, err = filepath.Abs(repoRoot)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry lint: %v\n", err)
		os.Exit(1)
	}

	issues, err := lintRepository(repoRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "telemetry lint: %v\n", err)
		os.Exit(1)
	}

	for _, current := range issues {
		fmt.Fprintf(
			os.Stderr,
			"%s:%d: %s %q is not documented in %s\n",
			relativePath(repoRoot, current.def.source),
			current.def.line,
			current.def.kind,
			current.def.value,
			relativePath(repoRoot, current.doc),
		)
	}

	if len(issues) > 0 {
		fmt.Fprintf(
			os.Stderr,
			"telemetry lint: %d undocumented telemetry item(s)\n",
			len(issues),
		)
		os.Exit(1)
	}

	fmt.Println("Telemetry documentation is up to date.")
}

func findRepoRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}

	for {
		if fileExists(filepath.Join(current, filepath.FromSlash(eventsSource))) &&
			fileExists(filepath.Join(current, filepath.FromSlash(schemaDoc))) {
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return "", fmt.Errorf("repository root not found from %q", start)
}

func lintRepository(repoRoot string) ([]issue, error) {
	documents, err := loadDocuments(
		filepath.Join(repoRoot, filepath.FromSlash(referenceDoc)),
		filepath.Join(repoRoot, filepath.FromSlash(schemaDoc)),
	)
	if err != nil {
		return nil, err
	}

	eventDefinitions, err := parseEvents(
		filepath.Join(repoRoot, filepath.FromSlash(eventsSource)),
	)
	if err != nil {
		return nil, err
	}

	fieldDefinitions, err := parseFields(
		filepath.Join(repoRoot, filepath.FromSlash(fieldsSource)),
	)
	if err != nil {
		return nil, err
	}

	rawFieldDefinitions, err := parseRawAttributes(
		filepath.Join(repoRoot, "cli", "azd"),
		filepath.Join(repoRoot, filepath.FromSlash(fieldsSource)),
	)
	if err != nil {
		return nil, err
	}

	literalEventDefinitions, err := parseLiteralEvents(
		filepath.Join(repoRoot, "cli", "azd"),
	)
	if err != nil {
		return nil, err
	}

	coreDefinitions := uniqueDefinitions(append(
		append(eventDefinitions, fieldDefinitions...),
		append(rawFieldDefinitions, literalEventDefinitions...)...,
	))
	issues := checkDefinitionsInEveryDocument(coreDefinitions, documents)

	extensionUsages, err := parseExtensionUsages(
		filepath.Join(repoRoot, "cli", "azd", "extensions"),
	)
	if err != nil {
		return nil, err
	}

	for _, usage := range extensionUsages {
		extensionDocuments, err := loadMarkdownDocuments(usage.root)
		if err != nil {
			return nil, err
		}
		issues = append(issues, checkDefinitionsInAnyDocument(
			usage.definitions,
			extensionDocuments,
			filepath.Join(usage.root, "README.md"),
		)...)
	}

	sortIssues(issues, repoRoot)
	return issues, nil
}

func loadDocuments(paths ...string) ([]document, error) {
	documents := make([]document, 0, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		documents = append(documents, newDocument(path, string(content)))
	}
	return documents, nil
}

func loadMarkdownDocuments(root string) ([]document, error) {
	var documents []document
	err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		if strings.EqualFold(filepath.Base(path), "changelog.md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		documents = append(documents, newDocument(path, string(content)))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read Markdown under %s: %w", root, err)
	}

	sort.Slice(documents, func(i, j int) bool {
		return documents[i].path < documents[j].path
	})
	return documents, nil
}

func parseEvents(path string) ([]definition, error) {
	file, fileSet, err := parseGoFile(path)
	if err != nil {
		return nil, err
	}

	var definitions []definition
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}
		for _, specification := range general.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range values.Names {
				if index >= len(values.Values) {
					continue
				}
				value, ok := stringLiteral(values.Values[index])
				if !ok {
					continue
				}
				definitions = append(definitions, definition{
					kind:   "event",
					value:  value,
					source: path,
					line:   fileSet.Position(name.Pos()).Line,
				})
			}
		}
	}
	return uniqueDefinitions(definitions), nil
}

func parseFields(path string) ([]definition, error) {
	file, fileSet, err := parseGoFile(path)
	if err != nil {
		return nil, err
	}

	var definitions []definition
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, specification := range general.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, name := range values.Names {
				if index >= len(values.Values) {
					continue
				}
				keys, err := fieldKeys(
					values.Values[index],
					name.Name,
					file,
					fileSet,
				)
				if err != nil {
					return nil, fmt.Errorf("%s:%d: %w",
						path, fileSet.Position(name.Pos()).Line, err)
				}
				for _, key := range keys {
					definitions = append(definitions, definition{
						kind:   "field",
						value:  key,
						source: path,
						line:   fileSet.Position(name.Pos()).Line,
					})
				}
			}
		}
	}
	return uniqueDefinitions(definitions), nil
}

func fieldKeys(
	expression ast.Expr,
	name string,
	file *ast.File,
	fileSet *token.FileSet,
) ([]string, error) {
	switch current := expression.(type) {
	case *ast.CompositeLit:
		var keys []string
		for _, element := range current.Elts {
			keyValue, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			keyName, ok := keyValue.Key.(*ast.Ident)
			if !ok || keyName.Name != "Key" {
				continue
			}
			key, err := resolveKeyExpression(
				keyValue.Value,
				file,
				fileSet,
			)
			if err != nil {
				return nil, err
			}
			if key != "" {
				keys = append(keys, key)
			}
		}
		return keys, nil
	case *ast.CallExpr:
		if !isAttributeKeyCall(current) || len(current.Args) == 0 {
			return nil, nil
		}
		key, err := resolveKeyExpression(current.Args[0], file, fileSet)
		if err != nil {
			return nil, err
		}
		if key == "" && name == "ObjectIdKey" {
			return []string{"user_AuthenticatedId"}, nil
		}
		if key == "" {
			return nil, fmt.Errorf(
				"could not resolve telemetry field key for %s", name)
		}
		return []string{key}, nil
	default:
		return nil, nil
	}
}

func resolveKeyExpression(
	expression ast.Expr,
	file *ast.File,
	fileSet *token.FileSet,
) (string, error) {
	if value, ok := stringLiteral(expression); ok {
		return value, nil
	}

	if call, ok := expression.(*ast.CallExpr); ok {
		if !isAttributeKeyCall(call) || len(call.Args) == 0 {
			return "", nil
		}
		return resolveKeyExpression(call.Args[0], file, fileSet)
	}

	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return "", nil
	}

	packageName, ok := selector.X.(*ast.Ident)
	if !ok {
		return "", nil
	}
	if packageName.Name == "contracts" &&
		selector.Sel.Name == "UserAuthUserId" {
		return "user_AuthenticatedId", nil
	}
	if packageName.Name != "semconv" {
		return "", nil
	}

	if key, ok := semanticConventionKeys[selector.Sel.Name]; ok {
		return key, nil
	}

	key := inlineCommentKey(file, fileSet, expression.Pos())
	if key == "" {
		return "", fmt.Errorf(
			"could not resolve semantic convention key %s",
			selector.Sel.Name,
		)
	}
	return key, nil
}

var semanticConventionKeys = map[string]string{
	"JSONRPCRequestIDKey": "rpc.jsonrpc.request_id",
	"RPCMethodKey":        "rpc.method",
}

func inlineCommentKey(
	file *ast.File,
	fileSet *token.FileSet,
	position token.Pos,
) string {
	line := fileSet.Position(position).Line
	for _, group := range file.Comments {
		if fileSet.Position(group.Pos()).Line != line {
			continue
		}
		for word := range strings.FieldsSeq(group.Text()) {
			word = strings.Trim(word, "`\"'.,;:()[]{}")
			if strings.Contains(word, ".") {
				return word
			}
		}
	}
	return ""
}

func parseRawAttributes(root, fieldsPath string) ([]definition, error) {
	var definitions []definition
	err := walkGoFiles(root, func(path string) error {
		if samePath(path, fieldsPath) {
			return nil
		}

		file, fileSet, err := parseGoFile(path)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || !isLiteralAttributeCall(call) {
				return true
			}
			value, ok := stringLiteral(call.Args[0])
			if !ok {
				return true
			}
			definitions = append(definitions, definition{
				kind:   "field",
				value:  value,
				source: path,
				line:   fileSet.Position(call.Pos()).Line,
			})
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return uniqueDefinitions(definitions), nil
}

func parseLiteralEvents(root string) ([]definition, error) {
	var definitions []definition
	err := walkGoFiles(root, func(path string) error {
		file, fileSet, err := parseGoFile(path)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Start" {
				return true
			}
			packageName, ok := selector.X.(*ast.Ident)
			if !ok || packageName.Name != "tracing" {
				return true
			}
			value, ok := stringLiteral(call.Args[1])
			if !ok {
				return true
			}
			definitions = append(definitions, definition{
				kind:   "event",
				value:  value,
				source: path,
				line:   fileSet.Position(call.Pos()).Line,
			})
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return uniqueDefinitions(definitions), nil
}

func parseExtensionUsages(root string) ([]extensionUsage, error) {
	byRoot := map[string][]definition{}
	staticData := map[string]*extensionStaticData{}
	var files []extensionSourceFile

	err := walkGoFiles(root, func(path string) error {
		extensionRoot := firstPathSegment(root, path)
		file, fileSet, err := parseGoFile(path)
		if err != nil {
			return err
		}

		dataKey := extensionDataKey(extensionRoot, path)
		data := staticData[dataKey]
		if data == nil {
			data = &extensionStaticData{
				constants:                  make(map[string]string),
				attributeMaps:              make(map[string][]definition),
				attributeMapReferences:     make(map[attributeMapReferenceKey][]definition),
				attributeMapReferenceKnown: make(map[attributeMapReferenceKey]bool),
			}
			staticData[dataKey] = data
		}
		collectStringConstants(file, data.constants)
		files = append(files, extensionSourceFile{
			root:    extensionRoot,
			path:    path,
			file:    file,
			fileSet: fileSet,
			data:    data,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	for {
		changed := false
		for _, source := range files {
			changed = collectStringConstants(
				source.file,
				source.data.constants,
			) || changed
		}
		if !changed {
			break
		}
	}

	for _, source := range files {
		collectAttributeMaps(
			source.file,
			source.path,
			source.fileSet,
			source.data,
		)
	}

	for _, source := range files {
		ast.Inspect(source.file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}

			var definitions []definition
			switch {
			case isReportUsageRequest(literal.Type):
				definitions = reportUsageDefinitions(
					literal, source.path, source.fileSet, source.data)
			case isTelemetryEventLiteral(source.file, literal):
				definitions = telemetryEventDefinitions(
					literal, source.path, source.fileSet, source.data)
			default:
				return true
			}

			byRoot[source.root] = append(
				byRoot[source.root],
				definitions...,
			)
			return true
		})
	}

	roots := make([]string, 0, len(byRoot))
	for root := range byRoot {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	usages := make([]extensionUsage, 0, len(roots))
	for _, extensionRoot := range roots {
		usages = append(usages, extensionUsage{
			root:        extensionRoot,
			definitions: uniqueDefinitions(byRoot[extensionRoot]),
		})
	}
	return usages, nil
}

func extensionDataKey(root, path string) string {
	return root + "\x00" + filepath.Dir(path)
}

func reportUsageDefinitions(
	literal *ast.CompositeLit,
	path string,
	fileSet *token.FileSet,
	data *extensionStaticData,
) []definition {
	return usageDefinitions(
		literal,
		"extension event",
		"extension field",
		path,
		fileSet,
		data,
	)
}

func telemetryEventDefinitions(
	literal *ast.CompositeLit,
	path string,
	fileSet *token.FileSet,
	data *extensionStaticData,
) []definition {
	return usageDefinitions(
		literal,
		"extension event",
		"extension field",
		path,
		fileSet,
		data,
	)
}

func usageDefinitions(
	literal *ast.CompositeLit,
	eventKind string,
	fieldKind string,
	path string,
	fileSet *token.FileSet,
	data *extensionStaticData,
) []definition {
	var definitions []definition
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		fieldName, ok := keyValue.Key.(*ast.Ident)
		if !ok {
			continue
		}

		switch fieldName.Name {
		case "EventName":
			if value, ok := resolveExtensionString(
				keyValue.Value, data.constants,
			); ok {
				definitions = append(definitions, definition{
					kind:   eventKind,
					value:  value,
					source: path,
					line:   fileSet.Position(keyValue.Pos()).Line,
				})
			}
		case "Name":
			if value, ok := resolveExtensionString(
				keyValue.Value, data.constants,
			); ok {
				definitions = append(definitions, definition{
					kind:   eventKind,
					value:  value,
					source: path,
					line:   fileSet.Position(keyValue.Pos()).Line,
				})
			}
		case "Attributes":
			definitions = append(
				definitions,
				attributeDefinitions(
					keyValue.Value,
					fieldKind,
					path,
					fileSet,
					data,
				)...,
			)
		}
	}
	return definitions
}

func collectStringConstants(
	file *ast.File,
	constants map[string]string,
) bool {
	changed := false
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.CONST {
			continue
		}

		var previousValues []ast.Expr
		for _, specification := range general.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}

			expressions := values.Values
			if len(expressions) == 0 {
				expressions = previousValues
			} else {
				previousValues = expressions
			}
			for index, name := range values.Names {
				if index >= len(expressions) {
					continue
				}
				if value, ok := resolveExtensionString(
					expressions[index], constants,
				); ok {
					if existing, exists := constants[name.Name]; !exists || existing != value {
						constants[name.Name] = value
						changed = true
					}
				}
			}
		}
	}
	return changed
}

func collectAttributeMaps(
	file *ast.File,
	path string,
	fileSet *token.FileSet,
	data *extensionStaticData,
) {
	packageValueSpecs := make(map[*ast.ValueSpec]bool)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, specification := range general.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if ok {
				packageValueSpecs[values] = true
			}
		}
	}

	var nodes []ast.Node
	var scopes []*attributeScope
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			last := nodes[len(nodes)-1]
			nodes = nodes[:len(nodes)-1]
			if startsAttributeScope(last) {
				scopes = scopes[:len(scopes)-1]
			}
			return true
		}

		nodes = append(nodes, node)
		switch current := node.(type) {
		case *ast.FuncDecl:
			scope := newAttributeScope()
			bindAttributeFields(scope, current.Recv)
			bindAttributeFields(scope, current.Type.Params)
			bindAttributeFields(scope, current.Type.Results)
			scopes = append(scopes, scope)
		case *ast.FuncLit:
			scope := newAttributeScope()
			bindAttributeFields(scope, current.Type.Params)
			bindAttributeFields(scope, current.Type.Results)
			scopes = append(scopes, scope)
		case *ast.BlockStmt, *ast.IfStmt, *ast.ForStmt,
			*ast.SwitchStmt, *ast.TypeSwitchStmt,
			*ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
			scopes = append(scopes, newAttributeScope())
		case *ast.ValueSpec:
			for index, name := range current.Names {
				var definitions []definition
				if index < len(current.Values) {
					definitions, _ = attributeDefinitionsFromMap(
						current.Values[index], path, fileSet, data,
					)
				}
				if packageValueSpecs[current] {
					data.attributeMaps[name.Name] = definitions
				} else if scope := currentAttributeScope(scopes); scope != nil {
					scope.bindings[name.Name] = definitions
				}
			}
		case *ast.AssignStmt:
			for index, left := range current.Lhs {
				if index >= len(current.Rhs) {
					continue
				}
				name, ok := left.(*ast.Ident)
				if !ok {
					continue
				}
				definitions, _ := attributeDefinitionsFromMap(
					current.Rhs[index], path, fileSet, data,
				)
				setAttributeBinding(
					scopes,
					data,
					name.Name,
					definitions,
					current.Tok == token.DEFINE,
				)
			}
		case *ast.RangeStmt:
			scopes = append(scopes, newAttributeScope())
			if current.Tok == token.DEFINE {
				scope := currentAttributeScope(scopes)
				if scope != nil {
					bindAttributeName(scope, current.Key)
					bindAttributeName(scope, current.Value)
				}
			}
		case *ast.Ident:
			if definitions, found := resolveAttributeBinding(
				scopes,
				data,
				current.Name,
			); found {
				key := attributeMapReferenceKey{
					path: path,
					pos:  current.Pos(),
				}
				data.attributeMapReferences[key] = definitions
				data.attributeMapReferenceKnown[key] = true
			}
		}
		return true
	})
}

func startsAttributeScope(node ast.Node) bool {
	switch node.(type) {
	case *ast.FuncDecl, *ast.FuncLit, *ast.BlockStmt,
		*ast.IfStmt, *ast.ForStmt, *ast.RangeStmt,
		*ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt,
		*ast.CaseClause, *ast.CommClause:
		return true
	default:
		return false
	}
}

func newAttributeScope() *attributeScope {
	return &attributeScope{bindings: make(map[string][]definition)}
}

func currentAttributeScope(scopes []*attributeScope) *attributeScope {
	if len(scopes) == 0 {
		return nil
	}
	return scopes[len(scopes)-1]
}

func bindAttributeFields(scope *attributeScope, fields *ast.FieldList) {
	if fields == nil {
		return
	}
	for _, field := range fields.List {
		for _, name := range field.Names {
			scope.bindings[name.Name] = nil
		}
	}
}

func bindAttributeName(scope *attributeScope, expression ast.Expr) {
	name, ok := expression.(*ast.Ident)
	if ok {
		scope.bindings[name.Name] = nil
	}
}

func setAttributeBinding(
	scopes []*attributeScope,
	data *extensionStaticData,
	name string,
	definitions []definition,
	declare bool,
) {
	if declare {
		if scope := currentAttributeScope(scopes); scope != nil {
			scope.bindings[name] = definitions
		}
		return
	}

	for index := len(scopes) - 1; index >= 0; index-- {
		if _, exists := scopes[index].bindings[name]; exists {
			scopes[index].bindings[name] = definitions
			return
		}
	}
	if _, exists := data.attributeMaps[name]; exists {
		data.attributeMaps[name] = definitions
	}
}

func resolveAttributeBinding(
	scopes []*attributeScope,
	data *extensionStaticData,
	name string,
) ([]definition, bool) {
	for index := len(scopes) - 1; index >= 0; index-- {
		if definitions, exists := scopes[index].bindings[name]; exists {
			return definitions, true
		}
	}
	definitions, exists := data.attributeMaps[name]
	return definitions, exists
}

func attributeDefinitions(
	expression ast.Expr,
	kind string,
	path string,
	fileSet *token.FileSet,
	data *extensionStaticData,
) []definition {
	if name, ok := expression.(*ast.Ident); ok {
		key := attributeMapReferenceKey{path: path, pos: name.Pos()}
		if data.attributeMapReferenceKnown[key] {
			return definitionsWithKind(
				data.attributeMapReferences[key],
				kind,
			)
		}
		return definitionsWithKind(data.attributeMaps[name.Name], kind)
	}

	definitions, ok := attributeDefinitionsFromMap(
		expression, path, fileSet, data,
	)
	if !ok {
		return nil
	}
	return definitionsWithKind(definitions, kind)
}

func attributeDefinitionsFromMap(
	expression ast.Expr,
	path string,
	fileSet *token.FileSet,
	data *extensionStaticData,
) ([]definition, bool) {
	attributes, ok := expression.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	if _, ok := attributes.Type.(*ast.MapType); !ok {
		return nil, false
	}

	var definitions []definition
	for _, element := range attributes.Elts {
		attribute, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		value, ok := resolveExtensionString(
			attribute.Key, data.constants,
		)
		if !ok {
			continue
		}
		definitions = append(definitions, definition{
			kind:   "extension field",
			value:  value,
			source: path,
			line:   fileSet.Position(attribute.Pos()).Line,
		})
	}
	return definitions, true
}

func definitionsWithKind(
	definitions []definition,
	kind string,
) []definition {
	if len(definitions) == 0 {
		return nil
	}

	result := make([]definition, len(definitions))
	copy(result, definitions)
	for index := range result {
		result[index].kind = kind
	}
	return result
}

func resolveExtensionString(
	expression ast.Expr,
	constants map[string]string,
) (string, bool) {
	switch current := expression.(type) {
	case *ast.BasicLit:
		return stringLiteral(current)
	case *ast.Ident:
		value, ok := constants[current.Name]
		return value, ok
	case *ast.BinaryExpr:
		if current.Op != token.ADD {
			return "", false
		}
		left, leftOK := resolveExtensionString(current.X, constants)
		right, rightOK := resolveExtensionString(current.Y, constants)
		return left + right, leftOK && rightOK
	default:
		return "", false
	}
}

func isTelemetryEventLiteral(file *ast.File, literal *ast.CompositeLit) bool {
	selector, ok := literal.Type.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Event" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || !isImportedPackage(
		file,
		packageName.Name,
		"github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry",
	) {
		return false
	}

	var hasName, hasAttributes bool
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Name":
			hasName = true
		case "Attributes":
			hasAttributes = true
		}
	}
	return hasName && hasAttributes
}

func isImportedPackage(file *ast.File, packageName, importPath string) bool {
	for _, specification := range file.Imports {
		currentPath, err := strconv.Unquote(specification.Path.Value)
		if err != nil || currentPath != importPath {
			continue
		}

		if specification.Name != nil {
			return specification.Name.Name == packageName
		}
		return pathpkg.Base(currentPath) == packageName
	}
	return false
}

func newDocument(path, content string) document {
	return document{
		path:   path,
		values: extractDocumentedValues(content),
	}
}

func checkDefinitionsInEveryDocument(
	definitions []definition,
	documents []document,
) []issue {
	if len(documents) == 0 {
		var issues []issue
		for _, current := range definitions {
			issues = append(issues, issue{
				def: current,
				doc: filepath.Join(filepath.Dir(current.source), "README.md"),
			})
		}
		return issues
	}

	var issues []issue
	for _, current := range definitions {
		for _, doc := range documents {
			if !isDocumented(doc, current) {
				issues = append(issues, issue{
					def: current,
					doc: doc.path,
				})
			}
		}
	}
	return issues
}

func checkDefinitionsInAnyDocument(
	definitions []definition,
	documents []document,
	suggestedDocument string,
) []issue {
	var issues []issue
	for _, current := range definitions {
		documented := false
		for _, doc := range documents {
			if isDocumented(doc, current) {
				documented = true
				break
			}
		}
		if !documented {
			issues = append(issues, issue{
				def: current,
				doc: suggestedDocument,
			})
		}
	}
	return issues
}

func isDocumented(doc document, current definition) bool {
	if _, ok := doc.values[current.value]; ok {
		return true
	}

	if current.kind == "extension field" {
		_, ok := doc.values["ext."+current.value]
		return ok
	}

	if current.kind != "event" {
		return false
	}

	for _, prefix := range []string{"cmd.", "mcp.", "vsrpc."} {
		if !strings.HasPrefix(current.value, prefix) {
			continue
		}
		for value := range doc.values {
			if value == prefix ||
				(strings.HasPrefix(value, prefix) &&
					strings.HasPrefix(value[len(prefix):], "<") &&
					strings.HasSuffix(value, ">")) {
				return true
			}
		}
	}
	return false
}

func extractDocumentedValues(content string) map[string]struct{} {
	values := make(map[string]struct{})
	for index := 0; index < len(content); {
		if content[index] != '`' {
			index++
			continue
		}

		start := index
		for index < len(content) && content[index] == '`' {
			index++
		}
		delimiterLength := index - start
		delimiter := strings.Repeat("`", delimiterLength)
		end := strings.Index(content[index:], delimiter)
		if end < 0 {
			break
		}

		value := content[index : index+end]
		if delimiterLength < 3 &&
			!strings.ContainsAny(value, "\r\n") {
			values[value] = struct{}{}
		}
		index += end + delimiterLength
	}
	return values
}

func uniqueDefinitions(definitions []definition) []definition {
	byKey := map[string]definition{}
	for _, current := range definitions {
		key := current.kind + "\x00" + current.value
		existing, ok := byKey[key]
		if !ok || definitionLess(current, existing) {
			byKey[key] = current
		}
	}

	result := make([]definition, 0, len(byKey))
	for _, current := range byKey {
		result = append(result, current)
	}
	sort.Slice(result, func(i, j int) bool {
		return definitionLess(result[i], result[j])
	})
	return result
}

func sortIssues(issues []issue, repoRoot string) {
	sort.Slice(issues, func(i, j int) bool {
		leftSource := relativePath(repoRoot, issues[i].def.source)
		rightSource := relativePath(repoRoot, issues[j].def.source)
		if leftSource != rightSource {
			return leftSource < rightSource
		}
		if issues[i].def.line != issues[j].def.line {
			return issues[i].def.line < issues[j].def.line
		}
		if issues[i].def.value != issues[j].def.value {
			return issues[i].def.value < issues[j].def.value
		}
		return relativePath(repoRoot, issues[i].doc) <
			relativePath(repoRoot, issues[j].doc)
	})
}

func definitionLess(left, right definition) bool {
	if left.source != right.source {
		return left.source < right.source
	}
	if left.line != right.line {
		return left.line < right.line
	}
	return left.value < right.value
}

func walkGoFiles(root string, callback func(string) error) error {
	return filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") &&
			!strings.HasSuffix(path, "_test.go") {
			return callback(path)
		}
		return nil
	})
}

func parseGoFile(path string) (*ast.File, *token.FileSet, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(
		fileSet,
		path,
		nil,
		parser.ParseComments,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return file, fileSet, nil
}

func stringLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func isAttributeKeyCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Key" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "attribute"
}

func isLiteralAttributeCall(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "attribute" {
		return false
	}

	switch selector.Sel.Name {
	case "Bool", "BoolSlice", "Float64", "Float64Slice",
		"Int", "Int64", "Int64Slice", "IntSlice",
		"Key", "String", "StringSlice":
		return true
	default:
		return false
	}
}

func isReportUsageRequest(expression ast.Expr) bool {
	switch current := expression.(type) {
	case *ast.StarExpr:
		return isReportUsageRequest(current.X)
	case *ast.SelectorExpr:
		return current.Sel.Name == "ReportUsageRequest"
	case *ast.Ident:
		return current.Name == "ReportUsageRequest"
	default:
		return false
	}
}

func firstPathSegment(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return root
	}
	segment := relative
	if index := strings.IndexRune(relative, os.PathSeparator); index >= 0 {
		segment = relative[:index]
	}
	return filepath.Join(root, segment)
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(relative)
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbs, rightAbs)
	}
	return leftAbs == rightAbs
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
