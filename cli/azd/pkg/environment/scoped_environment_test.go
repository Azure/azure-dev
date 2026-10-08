// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentImplementsScopedEnvironment(t *testing.T) {
	t.Parallel()

	backing := New("test")
	var env ScopedEnvironment = backing
	require.Same(t, backing, env.BackingEnv())
	require.Same(t, backing.Config, env.GetConfig())

	env.SetSubscriptionId("subscription")
	env.SetLocation("location")
	env.DotenvSet(TenantIdEnvVarName, "tenant")
	env.SetServiceProperty("api", "ENDPOINT", "endpoint")

	require.Equal(t, "subscription", env.GetSubscriptionId())
	require.Equal(t, "location", env.GetLocation())
	require.Equal(t, "tenant", env.GetTenantId())
	require.Equal(t, "endpoint", env.GetServiceProperty("api", "ENDPOINT"))
	require.Equal(t, backing.Dotenv(), env.Dotenv())
	require.NoError(t, env.GetConfig().Set("test.key", "value"))
	value, found := backing.Config.GetString("test.key")
	require.True(t, found)
	require.Equal(t, "value", value)
}

func TestMappedScopedEnvironmentMapsProviderInputsAndOutputs(t *testing.T) {
	t.Setenv("MISSING_LOCAL", "stale-process-value")

	backing := NewWithValues("test", map[string]string{
		"A":             "one",
		"B":             "two",
		"SHARED_INPUT":  "input",
		"SHARED_OUTPUT": "old-output",
	})
	env := NewMappedScopedEnvironment(
		backing,
		map[string]string{
			"A":             "B",
			"B":             "A",
			"LOCAL_INPUT":   "SHARED_INPUT",
			"MISSING_LOCAL": "MISSING_SHARED",
		},
		map[string]string{"LOCAL_OUTPUT": "SHARED_OUTPUT"},
	)

	require.Equal(t, "two", env.Getenv("A"))
	require.Equal(t, "one", env.Getenv("B"))
	require.Equal(t, "input", env.Getenv("LOCAL_INPUT"))
	require.Empty(t, env.Getenv("MISSING_LOCAL"))
	require.Equal(t, "", env.Dotenv()["MISSING_LOCAL"])

	env.DotenvSet("LOCAL_OUTPUT", "new-output")
	require.Equal(t, "new-output", backing.Getenv("SHARED_OUTPUT"))
	require.Empty(t, backing.Getenv("LOCAL_OUTPUT"))

	env.DotenvDelete("LOCAL_OUTPUT")
	require.Empty(t, backing.Getenv("SHARED_OUTPUT"))
}
