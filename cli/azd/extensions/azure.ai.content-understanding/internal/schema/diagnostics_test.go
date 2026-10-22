// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidationOrderedDiagnosticsAndSafePaths(t *testing.T) {
	const body = `{
		"baseAnalyzerId":"prebuilt-document",
		"config":{"contentCategories":{},"enableSegment":"private-value"},
		"fieldSchema":{"fields":{
			"z-private":{"type":"array","description":"A complete prompt for extraction."},
			"a-private":{"$ref":"private-value"}
		}}
	}`
	expected := ValidationResult{
		Errors: []Finding{
			{"config.enableSegment", "The value must be a boolean."},
			{"config.contentCategories", "Content categories must be a nonempty object."},
			{"fieldSchema.fields.a-private.$ref", "References are unsupported; inline the field definition."},
			{"fieldSchema.fields.z-private.items", "Array fields require an items object."},
		},
		Warnings: []Finding{
			{"models.completion", "Field extraction needs a completion model, for example gpt-5.2."},
		},
	}
	for _, version := range []string{"2025-11-01", "2026-06-01-preview"} {
		t.Run(version, func(t *testing.T) {
			result, err := Validate([]byte(body), version, false)
			require.NoError(t, err)
			require.Equal(t, expected, result)
			require.Equal(t, "[redacted field path]", result.Errors[0].DiagnosticPath())
			require.Equal(t, "config.contentCategories", result.Errors[1].DiagnosticPath())
			for _, finding := range append(result.Errors, result.Warnings...) {
				require.NotContains(t, finding.Message, "private")
				require.NotContains(t, finding.DiagnosticPath(), "private")
			}
		})
	}
}

func TestFindingDiagnosticPathAllowlist(t *testing.T) {
	for _, path := range []string{
		"$", "analyzerId", "baseAnalyzerId", "apiVersion", "models", "models.completion",
		"config", "config.contentCategories", "fieldSchema", "fieldSchema.fields",
	} {
		t.Run(path, func(t *testing.T) {
			require.Equal(t, path, (Finding{Path: path}).DiagnosticPath())
		})
	}
	for _, path := range []string{"", "private", "fieldSchema.fields.private", "config.contentCategories.private"} {
		t.Run("redact/"+path, func(t *testing.T) {
			require.Equal(t, "[redacted field path]", (Finding{Path: path}).DiagnosticPath())
		})
	}
}
