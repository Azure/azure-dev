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
	path    string
	content string
}

type issue struct {
	def definition
	doc string
}

type extensionUsage struct {
	root        string
	definitions []definition
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
	issues := checkDefinitions(coreDefinitions, documents)

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
		issues = append(
			issues,
			checkDefinitions(usage.definitions, extensionDocuments)...,
		)
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
		documents = append(documents, document{
			path:    path,
			content: string(content),
		})
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
		documents = append(documents, document{
			path:    path,
			content: string(content),
		})
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
	err := walkGoFiles(root, func(path string) error {
		file, fileSet, err := parseGoFile(path)
		if err != nil {
			return err
		}

		extensionRoot := firstPathSegment(root, path)
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isReportUsageRequest(literal.Type) {
				return true
			}
			definitions := reportUsageDefinitions(literal, path, fileSet)
			byRoot[extensionRoot] = append(
				byRoot[extensionRoot],
				definitions...,
			)
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
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

func reportUsageDefinitions(
	literal *ast.CompositeLit,
	path string,
	fileSet *token.FileSet,
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
			if value, ok := stringLiteral(keyValue.Value); ok {
				definitions = append(definitions, definition{
					kind:   "extension event",
					value:  value,
					source: path,
					line:   fileSet.Position(keyValue.Pos()).Line,
				})
			}
		case "Attributes":
			attributes, ok := keyValue.Value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, attributeElement := range attributes.Elts {
				attribute, ok := attributeElement.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				value, ok := stringLiteral(attribute.Key)
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
		}
	}
	return definitions
}

func checkDefinitions(
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
			if !isDocumented(doc.content, current.value) {
				issues = append(issues, issue{
					def: current,
					doc: doc.path,
				})
			}
		}
	}
	return issues
}

func isDocumented(content, value string) bool {
	if strings.Contains(content, value) {
		return true
	}

	for _, prefix := range []string{"cmd.", "mcp.", "vsrpc."} {
		if strings.HasPrefix(value, prefix) &&
			strings.Contains(content, "`"+prefix) {
			return true
		}
	}
	return false
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
