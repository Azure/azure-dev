// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package environment

import (
	"fmt"
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

func TestMappedScopedEnvironmentTranslatesProviderViewAndProjectView(t *testing.T) {
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

func TestMappedScopedEnvironmentExcludesAliasedLoaderControls(t *testing.T) {
	tests := []struct {
		name    string
		blocked bool
	}{
		{name: "LD_PRELOAD", blocked: true},
		{name: "LD_LIBRARY_PATH", blocked: true},
		{name: "LD_AUDIT", blocked: true},
		{name: "DYLD_INSERT_LIBRARIES", blocked: true},
		{name: "DYLD_LIBRARY_PATH", blocked: true},
		{name: "ld_preload", blocked: true},
		{name: "dyld_insert_libraries", blocked: true},
		{name: "SAFE_INPUT"},
		{name: "LDFLAGS"},
		{name: "LDLIBS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, processSource := range []bool{false, true} {
				t.Run(fmt.Sprintf("processSource=%t", processSource), func(t *testing.T) {
					backing := New("test")
					if processSource {
						t.Setenv("SHARED_INPUT", "value")
					} else {
						backing.DotenvSet("SHARED_INPUT", "value")
					}
					env := NewMappedScopedEnvironment(backing, map[string]string{tt.name: "SHARED_INPUT"}, nil)

					if tt.blocked {
						require.NotContains(t, env.Dotenv(), tt.name)
						require.NotContains(t, env.Environ(), tt.name+"=value")
					} else {
						require.Equal(t, "value", env.Dotenv()[tt.name])
						require.Contains(t, env.Environ(), tt.name+"=value")
					}
					require.Equal(t, "value", env.Getenv(tt.name), "direct lookups remain compatible")
					require.Equal(t, "value", backing.Getenv("SHARED_INPUT"))
				})
			}
		})
	}
}

// BACKCOMPAT: without aliases the mapped environment must be the backing environment itself, so providers see exactly
// what upstream/main gave them (same instance, same config, same saves). If this breaks, every provider that reaches
// for BackingEnv() or the live Config is affected.
func TestCompat_NoAliasesDoesNotWrapEnvironment(t *testing.T) {
	t.Parallel()

	backing := New("test")

	for _, env := range []ScopedEnvironment{
		NewMappedScopedEnvironment(backing, nil, nil),
		NewMappedScopedEnvironment(backing, map[string]string{}, map[string]string{}),
	} {
		require.Same(t, backing, env)
		require.Same(t, backing, env.BackingEnv())
	}
}

// BACKCOMPAT: even when aliases wrap the environment, the provider still reaches the live backing environment and
// its config directly (this is how Bicep saves and reads config). If this breaks, providers lose that access.
func TestCompat_MappedEnvironmentExposesBackingEnvironmentAndConfig(t *testing.T) {
	t.Parallel()

	backing := NewWithValues("test", map[string]string{"SHARED_INPUT": "input"})
	env := NewMappedScopedEnvironment(backing, map[string]string{"LOCAL": "SHARED_INPUT"}, nil)

	require.NotSame(t, backing, env)
	require.Same(t, backing, env.BackingEnv())
	require.Same(t, backing.Config, env.GetConfig())

	require.NoError(t, env.GetConfig().Set("infra.parameters.x", "y"))
	value, found := backing.Config.GetString("infra.parameters.x")
	require.True(t, found)
	require.Equal(t, "y", value)
}

// BACKCOMPAT: names that are not aliased pass straight through, including loader-control filtering that
// Environment.Dotenv() already applied upstream.
func TestCompat_UnaliasedNamesPassThrough(t *testing.T) {
	t.Parallel()

	backing := NewWithValues("test", map[string]string{"PLAIN": "p", "LD_PRELOAD": "evil"})
	env := NewMappedScopedEnvironment(backing, map[string]string{"LOCAL": "PLAIN"}, nil)

	require.Equal(t, "p", env.Getenv("PLAIN"))
	require.Equal(t, map[string]string{"PLAIN": "p", "LOCAL": "p"}, env.Dotenv())
	require.NotContains(t, env.Dotenv(), "LD_PRELOAD")
}
