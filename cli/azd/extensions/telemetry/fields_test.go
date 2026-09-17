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
//  3. every discovered key has exactly one declaration.
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
				"Declare every final ext.* key with its classification, purpose, and endpoint in "+
				"cli/azd/extensions/telemetry/fields.go. Define Attributes inline in a keyed "+
				"payload literal, with string-literal or same-package compile-time constant keys.",
			strings.Join(diagnostics, "\n"),
		)
	}
}

func TestValidateExtensionTelemetryUsages(t *testing.T) {
	t.Parallel()

	declarations := map[string]fieldDeclaration{
		"ext.shared.mode": {key: "ext.shared.mode"},
	}

	t.Run("declared field can be shared", func(t *testing.T) {
		diagnostics := validateExtensionTelemetryUsages(
			[]telemetryUsage{
				{extension: "contoso.first", key: "shared.mode"},
				{extension: "contoso.second", key: "shared.mode"},
			},
			declarations,
		)

		require.Empty(t, diagnostics)
	})

	t.Run("undeclared field is rejected", func(t *testing.T) {
		diagnostics := validateExtensionTelemetryUsages(
			[]telemetryUsage{{
				extension: "contoso.first",
				key:       "undeclared.mode",
				path:      "contoso.first/telemetry.go",
				line:      42,
			}},
			declarations,
		)

		require.Len(t, diagnostics, 1)
		require.Contains(t, diagnostics[0], `"ext.undeclared.mode" is not declared`)
	})
}

func validateExtensionTelemetryUsages(
	usages []telemetryUsage,
	declarations map[string]fieldDeclaration,
) []string {
	var diagnostics []string
	for _, usage := range usages {
		finalKey := fields.ExtensionAttributePrefix + usage.key
		if _, ok := declarations[finalKey]; ok {
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
