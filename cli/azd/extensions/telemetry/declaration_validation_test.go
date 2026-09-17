// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

const maxExtensionAttributeKeySize = 128

type fieldDeclaration struct {
	name           string
	key            string
	classification fields.Classification
	purpose        fields.Purpose
	endpoint       string
	isMeasurement  bool
	position       token.Position
}

// Declaration rules intentionally stay small and use the metadata categories
// already established by core azd fields.
func TestExtensionTelemetryDeclarationRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		declaration     string
		expectedMessage string
	}{
		{
			name: "valid declaration",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
		},
		{
			name: "public personal data classification is supported",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.PublicPersonalData,
	Purpose: fields.FeatureInsight,
	Endpoint: "ReviewedEndpoint",
}`,
		},
		{
			name: "public personal data requires endpoint metadata",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.PublicPersonalData,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "non-SystemMetadata classifications must use an endpoint other than N/A",
		},
		{
			name: "callstack or exception classification is supported",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.CallstackOrException,
	Purpose: fields.PerformanceAndHealth,
	Endpoint: "ReviewedEndpoint",
}`,
		},
		{
			name: "customer content is rejected",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.CustomerContent,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "CustomerContent is not allowed",
		},
		{
			name: "classification alias cannot hide customer content",
			declaration: `const SystemMetadata = fields.CustomerContent
var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "CustomerContent is not allowed",
		},
		{
			name: "classification is required",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "Classification must use a supported fields.Classification constant",
		},
		{
			name: "purpose is required",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Endpoint: "N/A",
}`,
			expectedMessage: "Purpose must use a supported fields.Purpose constant",
		},
		{
			name: "endpoint is required",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
}`,
			expectedMessage: "Endpoint must be set",
		},
		{
			name: "key uses final extension namespace",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "must use the final ext.* property name",
		},
		{
			name: "duplicate final key is rejected",
			declaration: `var FirstField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}
var SecondField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
}`,
			expectedMessage: "duplicates extension telemetry key",
		},
		{
			name: "parenthesized measurement",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
	IsMeasurement: (true),
}`,
			expectedMessage: "cannot be declared as measurements",
		},
		{
			name: "measurement constant",
			declaration: `const measurement = true
var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A",
	IsMeasurement: measurement,
}`,
			expectedMessage: "cannot be declared as measurements",
		},
		{
			name: "system metadata endpoint must be exact",
			declaration: `var TestField = fields.AttributeKey{
	Key: attribute.Key("ext.test"),
	Classification: fields.SystemMetadata,
	Purpose: fields.FeatureInsight,
	Endpoint: "N/A ",
}`,
			expectedMessage: "SystemMetadata must use endpoint N/A",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := `package telemetry
import (
	"go.opentelemetry.io/otel/attribute"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)
` + test.declaration
			path := filepath.Join(t.TempDir(), "fields.go")
			require.NoError(t, os.WriteFile(path, []byte(source), 0o600))

			_, diagnostics := loadFieldDeclarations(path)
			if test.expectedMessage == "" {
				require.Empty(t, diagnostics)
			} else {
				require.NotEmpty(t, diagnostics)
				require.Contains(t, strings.Join(diagnostics, "\n"), test.expectedMessage)
			}
		})
	}
}

func keyedCompositeLiteralValues(literal *ast.CompositeLit) map[string]ast.Expr {
	values := map[string]ast.Expr{}
	for _, element := range literal.Elts {
		keyValue, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := keyValue.Key.(*ast.Ident)
		if ok {
			values[key.Name] = keyValue.Value
		}
	}
	return values
}

func loadFieldDeclarations(path string) (map[string]fieldDeclaration, []string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: failed to parse declarations: %v", filepath.ToSlash(path), err)}
	}

	source := &sourceFile{
		path:       path,
		file:       file,
		imports:    importAliases(file),
		dotImports: dotImports(file),
	}
	pkg := &sourcePackage{files: []*sourceFile{source}}
	collectConstants(pkg)
	declarations := map[string]fieldDeclaration{}
	var diagnostics []string

	for _, declaration := range file.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}

		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for i, name := range valueSpec.Names {
				if !ast.IsExported(name.Name) || i >= len(valueSpec.Values) {
					continue
				}

				literal, ok := valueSpec.Values[i].(*ast.CompositeLit)
				if !ok || !isAttributeKeyType(literal.Type, source) {
					continue
				}

				field, fieldDiagnostics := parseFieldDeclaration(fset, source, pkg, name.Name, literal)
				diagnostics = append(diagnostics, fieldDiagnostics...)
				if field.key == "" {
					continue
				}
				if previous, exists := declarations[field.key]; exists {
					diagnostics = append(diagnostics, fmt.Sprintf(
						"%s:%d: %s duplicates extension telemetry key %q already declared by %s at line %d",
						filepath.ToSlash(path),
						field.position.Line,
						field.name,
						field.key,
						previous.name,
						previous.position.Line,
					))
					continue
				}
				declarations[field.key] = field
			}
		}
	}

	return declarations, diagnostics
}

func parseFieldDeclaration(
	fset *token.FileSet,
	source *sourceFile,
	pkg *sourcePackage,
	name string,
	literal *ast.CompositeLit,
) (fieldDeclaration, []string) {
	field := fieldDeclaration{name: name, position: fset.Position(literal.Pos())}
	values := keyedCompositeLiteralValues(literal)

	var diagnostics []string
	keyExpression := values["Key"]
	if call, ok := keyExpression.(*ast.CallExpr); ok && isAttributeKeyConversion(call, source) {
		keyExpression = call.Args[0]
	}
	if value, ok := resolveStringConstant(keyExpression, source, pkg, nil); ok {
		field.key = value
	} else {
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			"Key must be a compile-time string passed to attribute.Key",
		))
	}

	classificationName, classificationResolved := resolveCoreFieldConstant(
		values["Classification"],
		source,
		pkg,
		nil,
	)
	switch classificationName {
	case "PublicPersonalData":
		field.classification = fields.PublicPersonalData
	case "SystemMetadata":
		field.classification = fields.SystemMetadata
	case "CallstackOrException":
		field.classification = fields.CallstackOrException
	case "EndUserPseudonymizedInformation":
		field.classification = fields.EndUserPseudonymizedInformation
	case "OrganizationalIdentifiableInformation":
		field.classification = fields.OrganizationalIdentifiableInformation
	case "CustomerContent":
		classificationResolved = false
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			"CustomerContent is not allowed in extension telemetry",
		))
	default:
		classificationResolved = false
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			"Classification must use a supported fields.Classification constant",
		))
	}

	purposeName, _ := resolveCoreFieldConstant(
		values["Purpose"],
		source,
		pkg,
		nil,
	)
	switch purposeName {
	case "FeatureInsight":
		field.purpose = fields.FeatureInsight
	case "BusinessInsight":
		field.purpose = fields.BusinessInsight
	case "PerformanceAndHealth":
		field.purpose = fields.PerformanceAndHealth
	default:
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			"Purpose must use a supported fields.Purpose constant",
		))
	}

	if endpoint, ok := resolveStringConstant(values["Endpoint"], source, pkg, nil); ok {
		field.endpoint = endpoint
	}
	if field.endpoint == "" {
		diagnostics = append(diagnostics, declarationDiagnostic(field, "Endpoint must be set"))
	}

	if expression, exists := values["IsMeasurement"]; exists {
		value, ok := resolveBoolConstant(expression, source, pkg, nil)
		if !ok {
			diagnostics = append(diagnostics, declarationDiagnostic(
				field,
				"IsMeasurement must be a compile-time boolean",
			))
		} else {
			field.isMeasurement = value
		}
	}

	if !strings.HasPrefix(field.key, fields.ExtensionAttributePrefix) ||
		field.key == fields.ExtensionAttributePrefix {
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			fmt.Sprintf("Key %q must use the final ext.* property name", field.key),
		))
	} else if len([]byte(strings.TrimPrefix(
		field.key,
		fields.ExtensionAttributePrefix,
	))) > maxExtensionAttributeKeySize {
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			fmt.Sprintf("Key %q exceeds the ReportUsage key limit", field.key),
		))
	}

	if field.classification == fields.SystemMetadata && field.endpoint != "N/A" {
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			"SystemMetadata must use endpoint N/A",
		))
	}
	// Endpoint semantics require owner review; this source check enforces the public structural contract.
	if classificationResolved && field.classification != fields.SystemMetadata && field.endpoint == "N/A" {
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			"non-SystemMetadata classifications must use an endpoint other than N/A",
		))
	}
	if field.isMeasurement {
		diagnostics = append(diagnostics, declarationDiagnostic(
			field,
			"ReportUsage attributes are strings and cannot be declared as measurements",
		))
	}

	return field, diagnostics
}

func declarationDiagnostic(field fieldDeclaration, message string) string {
	return fmt.Sprintf(
		"%s:%d: extension telemetry field %s: %s",
		filepath.ToSlash(field.position.Filename),
		field.position.Line,
		field.name,
		message,
	)
}

func resolveCoreFieldConstant(
	expression ast.Expr,
	source *sourceFile,
	pkg *sourcePackage,
	resolving *constantResolution,
) (string, bool) {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return resolveCoreFieldConstant(value.X, source, pkg, resolving)
	case *ast.SelectorExpr:
		alias, ok := value.X.(*ast.Ident)
		if !ok ||
			alias.Obj != nil && alias.Obj.Kind != ast.Pkg ||
			source.imports[alias.Name] != tracingFieldsPackagePath {
			return "", false
		}
		return value.Sel.Name, true
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
			return resolveCoreFieldConstant(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			)
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
			current, ok := resolveCoreFieldConstant(
				definition.expression,
				definition.source,
				pkg,
				resolving,
			)
			if !ok || i > 0 && current != resolved {
				return "", false
			}
			resolved = current
		}
		return resolved, true
	default:
		return "", false
	}
}

func isAttributeKeyType(expression ast.Expr, source *sourceFile) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "AttributeKey" {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok &&
		(alias.Obj == nil || alias.Obj.Kind == ast.Pkg) &&
		source.imports[alias.Name] == tracingFieldsPackagePath
}

func isAttributeKeyConversion(call *ast.CallExpr, source *sourceFile) bool {
	if len(call.Args) != 1 {
		return false
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Key" {
		return false
	}
	alias, ok := selector.X.(*ast.Ident)
	return ok &&
		(alias.Obj == nil || alias.Obj.Kind == ast.Pkg) &&
		source.imports[alias.Name] == otelAttributePackagePath
}
