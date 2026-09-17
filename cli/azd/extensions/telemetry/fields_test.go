// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
)

// The contract is:
//  1. fields.go contains valid field declarations.
//  2. production extension source uses statically discoverable attribute keys.
//  3. every discovered extension/key pair has exactly one declaration.
func TestExtensionTelemetryDeclarationsAreValid(t *testing.T) {
	t.Parallel()

	telemetryDir := telemetryPackageDir(t)
	declarations, diagnostics := loadFieldDeclarations(filepath.Join(telemetryDir, "fields.go"))

	require.NotEmpty(t, declarations, "no extension telemetry fields were declared")
	require.Empty(t, diagnostics, "%s", strings.Join(diagnostics, "\n"))
}

func TestEveryExtensionTelemetryUsageIsDeclared(t *testing.T) {
	t.Parallel()

	telemetryDir := telemetryPackageDir(t)
	extensionRoot := filepath.Dir(telemetryDir)
	declarations, diagnostics := loadFieldDeclarations(filepath.Join(telemetryDir, "fields.go"))
	usages, scanDiagnostics := scanExtensionTelemetry(extensionRoot)
	diagnostics = append(diagnostics, scanDiagnostics...)
	diagnostics = append(diagnostics, validateExtensionTelemetryUsages(usages, declarations)...)

	sort.Strings(diagnostics)
	if len(diagnostics) > 0 {
		t.Fatalf(
			"extension telemetry validation failed:\n\n%s\n\n"+
				"Declare every final ext.* key with its owning extension, classification, purpose, and endpoint in "+
				"cli/azd/extensions/telemetry/fields.go. Define Attributes inline in a keyed "+
				"payload literal, with string-literal or same-package compile-time constant keys.",
			strings.Join(diagnostics, "\n"),
		)
	}
}

func TestExtensionTelemetryUsageMustMatchDeclarationOwner(t *testing.T) {
	t.Parallel()

	diagnostics := validateExtensionTelemetryUsages(
		[]telemetryUsage{{
			extension: "microsoft.azd.demo",
			key:       "route",
			path:      "microsoft.azd.demo/internal/cmd/telemetry.go",
			line:      42,
		}},
		map[string]fieldDeclaration{
			"ext.route": {
				extension: "azure.ai.agents",
				key:       "ext.route",
			},
		},
	)

	require.Len(t, diagnostics, 1)
	require.Contains(t, diagnostics[0], `"ext.route" is declared for "azure.ai.agents"`)
}

func validateExtensionTelemetryUsages(
	usages []telemetryUsage,
	declarations map[string]fieldDeclaration,
) []string {
	var diagnostics []string
	for _, usage := range usages {
		finalKey := fields.ExtensionAttributePrefix + usage.key
		declaration, ok := declarations[finalKey]
		if ok && declaration.extension == usage.extension {
			continue
		}

		if ok {
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s:%d: %s uses extension telemetry attribute %q, but %q is declared for %q in "+
					"cli/azd/extensions/telemetry/fields.go",
				usage.path,
				usage.line,
				usage.extension,
				usage.key,
				finalKey,
				declaration.extension,
			))
			continue
		}

		diagnostics = append(diagnostics, fmt.Sprintf(
			"%s:%d: %s uses extension telemetry attribute %q, but %q is not declared in "+
				"cli/azd/extensions/telemetry/fields.go",
			usage.path,
			usage.line,
			usage.extension,
			usage.key,
			finalKey,
		))
	}
	return diagnostics
}

func telemetryPackageDir(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to locate extension telemetry package")
	return filepath.Dir(filename)
}
