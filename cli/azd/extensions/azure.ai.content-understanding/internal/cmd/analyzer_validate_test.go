// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validationFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.json")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestAnalyzerValidateOfflineGoldenAndOutputFormats(t *testing.T) {
	f := newDefaultsFixture(t)
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_ACCESS_TOKEN", "private-host-token")
	t.Setenv("CU_ENDPOINT", "private-invalid-endpoint")
	t.Setenv("CU_API_KEY", "private-key")
	t.Setenv("CU_API_VERSION", "private-invalid-version")
	file := validationFile(t, `{"baseAnalyzerId":"prebuilt-document"}`)
	for _, format := range []string{"default", "json", "none"} {
		stdout, stderr, err := f.execute(t.Context(), "analyzer", "validate", file, "-o", format)
		require.NoError(t, err)
		assert.Empty(t, stderr)
		if format == "none" {
			assert.Empty(t, stdout)
		} else {
			assert.JSONEq(t, `{
				"ok":true,"errors":[],
				"warnings":[{"path":"fieldSchema","msg":"No fieldSchema; layout/markdown only."}],
				"strict":false,"apiVersion":"2025-11-01"
			}`, stdout)
		}
	}
	assert.Zero(t, f.opens)
	assert.Empty(t, f.client.calls)
}

func TestAnalyzerValidateStrictSpecAndVersion(t *testing.T) {
	f := newDefaultsFixture(t)
	file := validationFile(t, `{"description":5,"baseAnalyzerId":"prebuilt-document"}`)
	stdout, _, err := f.execute(t.Context(), "analyzer", "validate", "-s", file)
	require.NoError(t, err, "description is spec-only; curated root validation must not silently become spec validation")
	assert.Contains(t, stdout, `"ok": true`)
	stdout, _, err = f.execute(t.Context(), "analyzer", "validate", "-s", file, "--spec", "-o", "none")
	require.ErrorContains(t, err, "service contract")
	assert.Empty(t, stdout)
	file = validationFile(t, `{"baseAnalyzerId":"prebuilt-document"}`)
	stdout, _, err = f.execute(t.Context(), "analyzer", "validate", file, "--strict")
	require.ErrorContains(t, err, "fieldSchema")
	assert.Empty(t, stdout)
	file = validationFile(t, `{
		"apiVersion":"2026-06-01-preview","baseAnalyzerId":"prebuilt-document",
		"config":{"contentCategories":{"Invoice":{"description":"Invoices requesting payment."}}}
	}`)
	stdout, _, err = f.execute(t.Context(), "analyzer", "validate", file, "--strict", "--spec",
		"--api-version", cuclient.PreviewAPIVersion)
	require.NoError(t, err)
	assert.Contains(t, stdout, `"warnings": []`)
	assert.Contains(t, stdout, `"strict": true`)
	_, _, err = f.execute(t.Context(), "analyzer", "validate", file)
	require.ErrorContains(t, err, "apiVersion")
	assert.Zero(t, f.opens)
}

func TestAnalyzerValidateInvalidArgumentsAndContent(t *testing.T) {
	file := validationFile(t, `{"fieldSchema":{"fields":{"A":{"type":"private"},"B":{"type":"array"}}}}`)
	for _, args := range [][]string{
		{"analyzer", "validate"},
		{"analyzer", "validate", file, "--schema", file},
		{"analyzer", "validate", file, "extra"},
		{"analyzer", "validate", file, "--api-version", "private"},
		{"analyzer", "validate", file, "--endpoint", "private"},
		{"analyzer", "validate", file, "--profile", "private"},
		{"analyzer", "validate", file, "--json"},
		{"analyzer", "validate", file, "-o", "table"},
		{"analyzer", "validate", filepath.Join(t.TempDir(), "private-missing.json")},
		{"analyzer", "validate", t.TempDir()},
		{"analyzer", "validate", validationFile(t, "private-invalid-json")},
	} {
		f := newDefaultsFixture(t)
		stdout, stderr, err := f.execute(t.Context(), args...)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "private")
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
		assert.Zero(t, f.opens)
	}
	f := newDefaultsFixture(t)
	stdout, _, err := f.execute(t.Context(), "analyzer", "validate", file)
	require.Error(t, err)
	assert.Empty(t, stdout)
	assert.NotContains(t, err.Error(), "fieldSchema.fields")
	assert.Contains(t, err.Error(), "Specify a field type")
	assert.Contains(t, err.Error(), "Array fields require an items object")
	assert.NotContains(t, err.Error(), "private")
}

func TestAnalyzerValidateCancellationWriterAndMetadata(t *testing.T) {
	f := newDefaultsFixture(t)
	file := validationFile(t, `{}`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err := f.execute(ctx, "analyzer", "validate", file)
	require.ErrorIs(t, err, context.Canceled)
	root := f.root()
	root.SetOut(failingWriter{})
	root.SetArgs([]string{"analyzer", "validate", file})
	require.Error(t, root.ExecuteContext(t.Context()))
	stdout, _, err := f.execute(t.Context(), "metadata")
	require.NoError(t, err)
	var metadata extensions.ExtensionCommandMetadata
	require.NoError(t, json.Unmarshal([]byte(stdout), &metadata))
	command := extensions.ResolveCommand(&metadata, []string{"analyzer", "validate"})
	require.NotNil(t, command)
	require.NotNil(t, command.Launch)
	assert.True(t, command.Launch.NoAzdEnvironment)
	assert.Zero(t, f.opens)
}
