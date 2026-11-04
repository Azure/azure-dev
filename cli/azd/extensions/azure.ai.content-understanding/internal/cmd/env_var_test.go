// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"azure.ai.content-understanding/internal/config"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvironmentVariableListingIsAllowlistedAndSecretSafe(t *testing.T) {
	for _, key := range []string{"private-key", ""} {
		values := map[string]string{
			"CU_ENDPOINT": "https://configured.example/", "CU_API_KEY": key, "CU_AUTH_MODE": "key",
			"CU_ON_EXISTS": "skip", "AZD_ENVIRONMENT": "dev", "AZURE_TENANT_ID": "tenant",
			"AZD_ACCESS_TOKEN": "private-host-token", "AZURE_CLIENT_SECRET": "private-client-secret",
			"UNRELATED_VARIABLE": "private-unrelated", "CU_NO_UPDATE_CHECK": "private-unsupported",
			"FOUNDRY_ENDPOINT": "private-not-a-process-override",
		}
		list := listEnvironmentVariables(func(key string) (string, bool) {
			value, present := values[key]
			return value, present
		})
		require.Len(t, list, 6)
		assert.Equal(t, "CU_ENDPOINT", list[0].Name)
		encoded, err := json.Marshal(list)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "private")
		var found bool
		for _, variable := range list {
			if variable.Name == "CU_API_KEY" {
				found = true
				assert.True(t, variable.Sensitive)
				assert.Equal(t, "********", variable.Value)
			}
		}
		assert.True(t, found)
	}
	list := listEnvironmentVariables(func(string) (string, bool) { return "", false })
	assert.NotNil(t, list)
	assert.Empty(t, list)
}

func TestEnvironmentVariableEndpointDoesNotExposeMalformedSecrets(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:private@example.com", "https://example.com?sig=private",
		"https://example.com#private", "private-value", "",
	} {
		list := listEnvironmentVariables(func(name string) (string, bool) { return endpoint, name == "CU_ENDPOINT" })
		require.Len(t, list, 1)
		assert.Equal(t, "[invalid endpoint]", list[0].Value)
	}
}

func TestEnvironmentVariableDefinitionsCoverConfigurationInputs(t *testing.T) {
	names := map[string]bool{}
	for _, variable := range listEnvironmentVariables(func(string) (string, bool) { return "", true }) {
		assert.False(t, names[variable.Name], "variable must not be listed twice")
		names[variable.Name] = true
	}
	for name := range config.ProcessValues() {
		assert.True(t, names[name], "every supported configuration process input must be documented")
	}
}

func TestEnvironmentVariableCommandRemainsOfflineWithRejectedRuntimeKey(t *testing.T) {
	f := newDefaultsFixture(t)
	t.Setenv("AZD_SERVER", "")
	t.Setenv("AZD_ACCESS_TOKEN", "private-host-token")
	t.Setenv("CU_API_KEY", "private-resource-key")
	t.Setenv("CU_ENDPOINT", "https://example.com?sig=private-sas")
	t.Setenv("UNRELATED_VARIABLE", "private-unrelated")
	stdout, stderr, err := f.execute(t.Context(), "env-var", "list", "-o", "json")
	require.NoError(t, err)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, `"value": "********"`)
	assert.Contains(t, stdout, "[invalid endpoint]")
	assert.NotContains(t, stdout, "private")
	assert.Zero(t, f.opens)
	stdout, stderr, err = f.execute(t.Context(), "env-var", "list", "-o", "none")
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestEnvironmentVariableCancellationAndDiagnosticsOutputFailures(t *testing.T) {
	f := newDefaultsFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stdout, stderr, err := f.execute(ctx, "env-var", "list")
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
	assert.Zero(t, f.opens)

	for _, args := range [][]string{{"env-var", "list"}, {"doctor"}, {"defaults", "show"}, {
		"defaults", "set", "--model", "gpt-5.2=deployment",
	}} {
		f := newDefaultsFixture(t)
		root := f.root()
		root.SetOut(failingWriter{})
		root.SetArgs(args)
		require.Error(t, root.ExecuteContext(t.Context()))
		assert.Equal(t, f.opens, f.closes)
	}
}

func TestDiagnosticsMetadataAndHelpHaveNoProfileSurface(t *testing.T) {
	f := newDefaultsFixture(t)
	root := f.root()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"metadata"})
	require.NoError(t, root.ExecuteContext(t.Context()))
	var metadata extensions.ExtensionCommandMetadata
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &metadata))
	for _, path := range [][]string{{"doctor"}, {"defaults", "show"}, {"defaults", "set"}, {"env-var", "list"}} {
		command := extensions.ResolveCommand(&metadata, path)
		require.NotNil(t, command)
		require.NotNil(t, command.Launch)
		assert.True(t, command.Launch.NoAzdEnvironment)
	}
	for _, args := range [][]string{
		{"profile"}, {"doctor", "--profile", "dev"}, {"doctor", "--api-key", "private"},
		{"doctor", "--auth-mode", "key"}, {"doctor", "--fix-defaults", "extra"},
		{"env-var", "list", "--profile", "dev"}, {"env-var", "list", "--json"},
	} {
		stdout, stderr, err := f.execute(t.Context(), args...)
		require.Error(t, err)
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
		assert.NotContains(t, err.Error(), "private")
	}
	for _, path := range [][]string{{"doctor"}, {"defaults", "show"}, {"defaults", "set"}, {"env-var", "list"}} {
		_, stderr, err := f.execute(t.Context(), append(path, "--help")...)
		require.NoError(t, err)
		assert.Empty(t, stderr)
	}
	assert.Zero(t, f.opens)
}

func TestEnvironmentVariableDocumentationCoversListedInputs(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	require.NoError(t, err)
	require.Contains(t, string(readme),
		"(https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md)")
	guide, err := os.ReadFile("../../docs/usage.md")
	require.NoError(t, err)
	for _, variable := range listEnvironmentVariables(func(string) (string, bool) { return "", true }) {
		assert.Contains(t, string(guide), "`"+variable.Name+"`")
	}
}
