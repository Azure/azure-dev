// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package featureflags

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsEnabledValidation(t *testing.T) {
	for _, feature := range []Feature{
		{},
		{ID: ManagedHarness.ID},
		{EnvVar: ManagedHarness.EnvVar},
	} {
		t.Run(fmt.Sprintf("%+v", feature), func(t *testing.T) {
			enabled, err := IsEnabled(feature)
			require.False(t, enabled)
			require.ErrorContains(t, err, "feature ID and EnvVar must not be empty")
		})
	}
}

func TestIsEnabledWithoutDescription(t *testing.T) {
	feature := Feature{ID: ManagedHarness.ID, EnvVar: ManagedHarness.EnvVar}
	t.Setenv(feature.EnvVar, "true")

	enabled, err := IsEnabled(feature)

	require.NoError(t, err)
	require.True(t, enabled)
}

func TestIsEnabledDefaultsOff(t *testing.T) {
	for _, feature := range All() {
		t.Run(feature.ID, func(t *testing.T) {
			t.Setenv(feature.EnvVar, "")
			require.NoError(t, os.Unsetenv(feature.EnvVar))

			enabled, err := IsEnabled(feature)

			require.NoError(t, err)
			require.False(t, enabled)
			_, found := os.LookupEnv(feature.EnvVar)
			require.False(t, found)
		})
	}
}

func TestIsEnabledBooleanValues(t *testing.T) {
	values := []struct {
		value string
		want  bool
	}{
		{"1", true}, {"t", true}, {"T", true},
		{"true", true}, {"TRUE", true}, {"True", true},
		{"0", false}, {"f", false}, {"F", false},
		{"false", false}, {"FALSE", false}, {"False", false},
		{"", false}, {"off", false}, {"on", false},
		{"yes", false}, {"no", false}, {" true ", false},
		{" true", false}, {"true ", false}, {"TrUe", false},
		{"2", false}, {"invalid", false},
	}
	for _, feature := range All() {
		t.Run(feature.ID, func(t *testing.T) {
			for _, tt := range values {
				t.Run(fmt.Sprintf("%q", tt.value), func(t *testing.T) {
					t.Setenv(feature.EnvVar, tt.value)

					enabled, err := IsEnabled(feature)

					require.NoError(t, err)
					require.Equal(t, tt.want, enabled)
				})
			}
		})
	}
}

func TestIsEnabledFeaturesAreIndependent(t *testing.T) {
	features := All()
	for mask := range 1 << len(features) {
		t.Run(fmt.Sprintf("%03b", mask), func(t *testing.T) {
			for i, feature := range features {
				t.Setenv(feature.EnvVar, fmt.Sprint(mask&(1<<i) != 0))
			}

			for i, feature := range features {
				enabled, err := IsEnabled(feature)
				require.NoError(t, err)
				require.Equal(t, mask&(1<<i) != 0, enabled, feature.ID)
			}
		})
	}
}

func TestIsEnabledIgnoresAliases(t *testing.T) {
	for _, name := range []string{
		"AZD_FOUNDRY_EXT_ENABLE_AGENTS_MANAGED_HARNESS",
		"AZD_FOUNDRY_EXT_ENABLE_AGENTS_VOICE",
		"AZD_FOUNDRY_EXT_ENABLE_AGENTS_LONG_RUNNING",
		"AZD_FOUNDRY_EXT_ENABLE_ALL",
		"AZD_ALPHA_ENABLE_ALL",
		"AZD_ALPHA_ENABLE_AGENTS",
		"AZD_ALPHA_ENABLE_AGENTS_OTHER",
	} {
		t.Setenv(name, "true")
	}
	for _, feature := range All() {
		t.Run(feature.ID, func(t *testing.T) {
			t.Setenv(feature.EnvVar+"_", "true")
			for _, value := range []string{"", "false", "true"} {
				t.Setenv(feature.EnvVar, value)

				enabled, err := IsEnabled(feature)

				require.NoError(t, err)
				require.Equal(t, value == "true", enabled)
			}
		})
	}
}

func TestIsEnabledDoesNotLoadLocalConfiguration(t *testing.T) {
	// Direct calls do not open azd config or dotenv files on disk.
	// A value already in the process environment still enables the feature.
	// That is how a value the azd host injected from the selected environment is observed.
	// This test does not launch through the host.
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("AZD_CONFIG_DIR", filepath.Join(root, "config"))

	var project, dotenv strings.Builder
	project.WriteString("name: feature-flags-test\nservices:\n  agent:\n    host: azure.ai.agent\n    env:\n")
	for _, feature := range All() {
		fmt.Fprintf(&project, "      %s: \"true\"\n", feature.EnvVar)
		fmt.Fprintf(&dotenv, "%s=true\n", feature.EnvVar)
	}
	files := map[string]string{
		filepath.Join("config", "config.json"): `{"alpha":{"all":"on","agents":{` +
			`"managed_harness":"on","voice":"on","long_running":"on"}}}`,
		"azure.yaml":                               project.String(),
		filepath.Join(".azure", "config.json"):     `{"version":1,"defaultEnvironment":"default"}`,
		filepath.Join(".azure", "default", ".env"): dotenv.String(),
		filepath.Join(".azure", "selected", ".env"): "AZD_ALPHA_ENABLE_AGENTS_MANAGED_HARNESS=false\n" +
			"AZD_ALPHA_ENABLE_AGENTS_VOICE=false\nAZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING=false\n",
	}
	for path, contents := range files {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	}

	for _, environment := range []string{"", "default", "selected", "missing"} {
		t.Run("environment="+environment, func(t *testing.T) {
			t.Setenv("AZD_ENVIRONMENT", environment)
			for _, feature := range All() {
				for _, value := range []string{"", "false", "true"} {
					t.Setenv(feature.EnvVar, value)

					enabled, err := IsEnabled(feature)

					require.NoError(t, err)
					require.Equal(t, value == "true", enabled, feature.ID)
				}
			}
		})
	}

	for path, contents := range files {
		actual, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, contents, string(actual), path)
	}
}

func TestIsEnabledReadsCurrentEnvironmentWithoutMutation(t *testing.T) {
	for _, feature := range All() {
		t.Run(feature.ID, func(t *testing.T) {
			beforeFeature := feature
			for _, value := range []string{"true", "false", "1", "", "invalid", "True"} {
				t.Setenv(feature.EnvVar, value)
				t.Setenv("UNRELATED_FEATURE_FLAG_TEST", "unchanged")
				beforeEnvironment := os.Environ()

				enabled, err := IsEnabled(feature)

				require.NoError(t, err)
				require.Equal(t, value == "true" || value == "1" || value == "True", enabled)
				require.Equal(t, beforeFeature, feature)
				require.ElementsMatch(t, beforeEnvironment, os.Environ())
			}
		})
	}
}
