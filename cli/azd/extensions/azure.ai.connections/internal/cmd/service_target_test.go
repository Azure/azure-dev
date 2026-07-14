// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeploy_IsNoOp verifies the connection service target does not create the
// connection at deploy time. Connections declared as host: azure.ai.connection
// services are provisioned by the microsoft.foundry provider (synthesis) at
// provision time, so Deploy must return an empty result without any ARM call.
func TestDeploy_IsNoOp(t *testing.T) {
	t.Parallel()

	target := &connectionServiceTarget{}
	svc := &azdext.ServiceConfig{Name: "search-conn", Host: aiConnectionHost}

	var progressMsgs []string
	progress := func(msg string) { progressMsgs = append(progressMsgs, msg) }

	// A nil azdClient would panic if Deploy tried to reach the environment or
	// ARM; the no-op must not touch either.
	res, err := target.Deploy(t.Context(), svc, nil, nil, progress)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, progressMsgs, 1)
	assert.Contains(t, progressMsgs[0], "search-conn")
	assert.Contains(t, progressMsgs[0], "provisioned by infrastructure")
}

// TestPackagePublish_AreNoOps verifies the remaining lifecycle methods a
// connection has no build/publish artifact for return empty results.
func TestPackagePublish_AreNoOps(t *testing.T) {
	t.Parallel()

	target := &connectionServiceTarget{}
	svc := &azdext.ServiceConfig{Name: "search-conn", Host: aiConnectionHost}

	pkg, err := target.Package(t.Context(), svc, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, pkg)

	pub, err := target.Publish(t.Context(), svc, nil, nil, nil, nil)
	require.NoError(t, err)
	assert.NotNil(t, pub)

	endpoints, err := target.Endpoints(t.Context(), svc, nil)
	require.NoError(t, err)
<<<<<<< HEAD
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

	serviceConfig := &azdext.ServiceConfig{
		Environment: map[string]string{"SEARCH_KEY": "resolved-secret"},
	}
	environment, err := (&connectionServiceTarget{}).environmentValues(
		t.Context(),
		serviceConfig,
	)
	require.NoError(t, err)

	// Foundry server-side expressions pass through untouched.
	assert.Equal(t,
		"resolved-secret",
		resolveConnectionEnv(
			"${SEARCH_KEY}",
			environment,
		),
	)
	assert.Equal(t,
		"${{connections.x.credentials.key}}",
		resolveConnectionEnv(
			"${{connections.x.credentials.key}}",
			environment,
		),
	)
}

func TestConnectionMetadataPairs(t *testing.T) {
	t.Parallel()

	pairs := connectionMetadataPairs(
		map[string]string{"type": "gateway", "team": "search"},
		func(s string) string { return s },
	)
	assert.Equal(t, []string{"team=search", "type=gateway"}, pairs)
=======
	assert.Nil(t, endpoints)
>>>>>>> origin/main
}
