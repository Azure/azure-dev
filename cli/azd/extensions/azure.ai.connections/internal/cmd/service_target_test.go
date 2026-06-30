// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestParseConnectionServiceConfig_ServiceLevel(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"category": "ApiKey",
		"target":   "https://example.openai.azure.com",
		"authType": "ApiKey",
		"credentials": map[string]any{
			"key": "${SEARCH_KEY}",
		},
		"metadata": map[string]any{"team": "search"},
	})
	require.NoError(t, err)

	cfg, err := parseConnectionServiceConfig(&azdext.ServiceConfig{
		Name:                 "prod-search",
		Host:                 aiConnectionHost,
		AdditionalProperties: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "ApiKey", cfg.Category)
	assert.Equal(t, "https://example.openai.azure.com", cfg.Target)
	assert.Equal(t, "ApiKey", cfg.AuthType)
	assert.Equal(t, "${SEARCH_KEY}", cfg.Credentials["key"])
	assert.Equal(t, "search", cfg.Metadata["team"])
}

// TestParseConnectionServiceConfig_ConfigFallback verifies connections written before the
// per-resource service split (config-nested shape) still parse.
func TestParseConnectionServiceConfig_ConfigFallback(t *testing.T) {
	t.Parallel()

	props, err := structpb.NewStruct(map[string]any{
		"category": "CustomKeys",
		"authType": "CustomKeys",
	})
	require.NoError(t, err)

	cfg, err := parseConnectionServiceConfig(&azdext.ServiceConfig{
		Name:   "legacy",
		Host:   aiConnectionHost,
		Config: props,
	})
	require.NoError(t, err)
	assert.Equal(t, "CustomKeys", cfg.Category)
	assert.Equal(t, "CustomKeys", cfg.AuthType)
}

func TestConnectionCredentialArgs(t *testing.T) {
	t.Parallel()

	identity := func(s string) string { return s }

	t.Run("api-key reads the key credential", func(t *testing.T) {
		t.Parallel()
		key, customKeys := connectionCredentialArgs("api-key", map[string]any{"key": "secret"}, identity)
		assert.Equal(t, "secret", key)
		assert.Empty(t, customKeys)
	})

	t.Run("custom-keys renders sorted key=value pairs", func(t *testing.T) {
		t.Parallel()
		key, customKeys := connectionCredentialArgs(
			"custom-keys",
			map[string]any{"b-token": "2", "a-token": "1"},
			identity,
		)
		assert.Empty(t, key)
		assert.Equal(t, []string{"a-token=1", "b-token=2"}, customKeys)
	})
}

func TestResolveConnectionEnv(t *testing.T) {
	t.Parallel()

	env := map[string]string{"SEARCH_KEY": "resolved-secret"}

	// ${VAR} resolves from the azd env; Foundry ${{...}} passes through untouched.
	assert.Equal(t, "resolved-secret", resolveConnectionEnv("${SEARCH_KEY}", env))
	assert.Equal(t,
		"${{connections.x.credentials.key}}",
		resolveConnectionEnv("${{connections.x.credentials.key}}", env),
	)
}

func TestConnectionMetadataPairs(t *testing.T) {
	t.Parallel()

	pairs := connectionMetadataPairs(
		map[string]string{"type": "gateway", "team": "search"},
		func(s string) string { return s },
	)
	assert.Equal(t, []string{"team=search", "type=gateway"}, pairs)
}
