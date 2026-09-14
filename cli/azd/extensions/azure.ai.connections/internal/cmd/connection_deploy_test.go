// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"path/filepath"
	"testing"

	"azure.ai.connections/internal/definition"
	"azure.ai.connections/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootContainsConnectionDeploy(t *testing.T) {
	t.Parallel()

	command, _, err := NewRootCommand().Find([]string{"deploy"})
	require.NoError(t, err)
	assert.Equal(t, "deploy", command.Name())
}

func TestConnectionDeployFlags(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")

	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Name:        "github",
		Category:    "RemoteTool",
		Target:      "https://api.githubcopilot.com/mcp/",
		AuthType:    "CustomKeys",
		Credentials: map[string]any{"Authorization": "Bearer ${GITHUB_TOKEN}"},
		Metadata:    map[string]string{"owner": "platform"},
	}))

	flags, err := connectionDeployFlags(path, "json")
	require.NoError(t, err)
	assert.Equal(t, "github", flags.name)
	assert.Equal(t, "RemoteTool", flags.kind)
	assert.Equal(t, "custom-keys", flags.authType)
	assert.Equal(t, []string{"Authorization=Bearer test-token"}, flags.customKeys)
	assert.Equal(t, []string{"owner=platform"}, flags.metadata)
	assert.True(t, flags.force)
	assert.Equal(t, "json", flags.output)
}

func TestConnectionDeployFlagsOAuth2(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	// #nosec G101 -- the test fixture contains placeholder OAuth credentials only.
	require.NoError(t, definition.Save(path, &definition.Definition{
		Name:             "github-oauth",
		Category:         "RemoteTool",
		Target:           "https://api.githubcopilot.com/mcp/",
		AuthType:         "OAuth2",
		AuthorizationURL: "https://github.com/login/oauth/authorize",
		TokenURL:         "https://github.com/login/oauth/access_token",
		Credentials: map[string]any{
			"clientId": "client", "clientSecret": "secret",
		},
	}))

	flags, err := connectionDeployFlags(path, "table")
	require.NoError(t, err)
	assert.Equal(t, "oauth2", flags.authType)
	assert.Equal(t, "client", flags.clientID)
	assert.Equal(t, "secret", flags.clientSecret)
}

func TestConnectionDeployFlagsRequiresName(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{}))

	_, err := connectionDeployFlags(path, "json")
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeMissingConnectionField, localErr.Code)
}

func TestNormalizeAuthTypeAgenticIdentityAlias(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "agentic-identity", normalizeAuthType("AgenticIdentity"))
}

func TestConnectionDeployFlagsPreservesFoundryExpression(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), definition.DefaultPath)
	require.NoError(t, definition.Save(path, &definition.Definition{
		Name:        "dependent",
		Category:    "RemoteTool",
		Target:      "${{connections.source.target}}",
		AuthType:    "None",
		Credentials: map[string]any{},
	}))

	flags, err := connectionDeployFlags(path, "json")
	require.NoError(t, err)
	assert.Equal(t, "${{connections.source.target}}", flags.target)
}
