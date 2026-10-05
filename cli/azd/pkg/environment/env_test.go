// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentImplementsEnv(t *testing.T) {
	t.Parallel()

	backing := New("test")
	var env Env = backing
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
