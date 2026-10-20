// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package alpha

import (
	"fmt"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/require"
)

func TestGetFeature(t *testing.T) {
	t.Parallel()

	for _, expected := range allFeatures {
		actual, found := GetFeature(FeatureId(expected.Id))
		require.True(t, found)
		require.Equal(t, expected, actual)

		actual.Description = "changed copy"
		unchanged, found := GetFeature(FeatureId(expected.Id))
		require.True(t, found)
		require.Equal(t, expected, unchanged)
	}

	_, found := GetFeature("unknown.test.feature")
	require.False(t, found)
	_, found = GetFeature(AllId)
	require.False(t, found)
}

func TestFeature_EnvVarMetadata(t *testing.T) {
	t.Parallel()

	var features []Feature
	require.NoError(t, yaml.Unmarshal([]byte(`
- id: test.feature
  description: Test fixture only.
  envVar: AZD_FOUNDRY_EXT_ENABLE_AGENTS_TEST
- id: test.legacy
  description: Default env mapping.
`), &features))
	require.Len(t, features, 2)
	require.Equal(t, "AZD_FOUNDRY_EXT_ENABLE_AGENTS_TEST", features[0].EnvVar)
	require.Empty(t, features[1].EnvVar)
	require.True(t, features[0].IsEnabledWithSources(nil, map[string]string{
		features[0].EnvVar: "true",
	}))
	require.True(t, features[1].IsEnabledWithSources(nil, map[string]string{
		"AZD_ALPHA_ENABLE_TEST_LEGACY": "true",
	}))
}

func TestFeature_IsEnabledWithSources_Matrix(t *testing.T) {
	t.Parallel()

	feature := Feature{Id: "test.feature", EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_TEST"}

	// Four independent env maps, per-feature config, and alpha.all.
	for mask := range 1 << 6 {
		t.Run(fmt.Sprintf("%06b", mask), func(t *testing.T) {
			t.Parallel()

			sources := make([]map[string]string, 4)
			for i := range sources {
				value := "false"
				if mask&(1<<i) != 0 {
					value = "true"
				}
				sources[i] = map[string]string{feature.EnvVar: value}
			}

			userConfig := config.NewEmptyConfig()
			for i, id := range []string{feature.Id, string(AllId)} {
				value := "off"
				if mask&(1<<(i+4)) != 0 {
					value = "on"
				}
				require.NoError(t, userConfig.Set("alpha."+id, value))
			}

			require.Equal(t, mask != 0, feature.IsEnabledWithSources(userConfig, sources...))
		})
	}
}

func TestFeature_IsEnabledWithSources_EnvValues(t *testing.T) {
	t.Parallel()

	feature := Feature{Id: "test.feature", EnvVar: "AZD_FOUNDRY_EXT_ENABLE_TEST"}
	for _, value := range []string{
		"1", "t", "T", "TRUE", "true", "True",
		"0", "f", "F", "FALSE", "false", "False",
		"", "off", "on", "yes", "invalid", " true ", "TrUe",
	} {
		t.Run(fmt.Sprintf("%q", value), func(t *testing.T) {
			t.Parallel()

			source := map[string]string{feature.EnvVar: value}
			expected := value == "1" || value == "t" || value == "T" ||
				value == "TRUE" || value == "true" || value == "True"
			require.Equal(t, expected, feature.IsEnabledWithSources(nil, source))

			positive := map[string]string{feature.EnvVar: "true"}
			require.True(t, feature.IsEnabledWithSources(nil, positive, source))
			require.True(t, feature.IsEnabledWithSources(nil, source, positive))

			userConfig := config.NewEmptyConfig()
			require.NoError(t, userConfig.Set("alpha."+feature.Id, "on"))
			require.True(t, feature.IsEnabledWithSources(userConfig, source))
		})
	}

	require.False(t, feature.IsEnabledWithSources(nil))
	require.False(t, feature.IsEnabledWithSources(nil, nil, map[string]string{}))
}

func TestFeature_IsEnabledWithSources_ConfigValues(t *testing.T) {
	t.Parallel()

	feature := Feature{Id: "test.feature", EnvVar: "AZD_FOUNDRY_EXT_ENABLE_TEST"}
	for _, id := range []string{feature.Id, string(AllId)} {
		for _, value := range []any{"on", "ON", "On", "oN", "off", "", "true", "1", "invalid", true, 1, nil} {
			t.Run(fmt.Sprintf("%s/%v", id, value), func(t *testing.T) {
				t.Parallel()

				userConfig := config.NewEmptyConfig()
				require.NoError(t, userConfig.Set("alpha."+id, value))
				expected := value == "on" || value == "ON" || value == "On" || value == "oN"
				require.Equal(t, expected, feature.IsEnabledWithSources(userConfig))
				require.True(t, feature.IsEnabledWithSources(userConfig, map[string]string{
					feature.EnvVar: "true",
				}))
			})
		}
	}
}

func TestFeature_IsEnabledWithSources_ExplicitEnvOnly(t *testing.T) {
	t.Parallel()

	feature := Feature{Id: "test.feature", EnvVar: "AZD_FOUNDRY_EXT_ENABLE_AGENTS_TEST"}
	require.False(t, feature.IsEnabledWithSources(nil, map[string]string{
		"AZD_ALPHA_ENABLE_TEST.FEATURE":      "true",
		"AZD_ALPHA_ENABLE_TEST_FEATURE":      "true",
		"AZD_ALPHA_ENABLE_ALL":               "true",
		"AZD_FOUNDRY_EXT_ENABLE_ALL":         "true",
		"AZD_FOUNDRY_EXT_ENABLE_MODELS_TEST": "true",
		"AZD_FOUNDRY_EXT_ENABLE_TEST":        "true",
	}))
}

func TestFeature_IsEnabledWithSources_DefaultEnvNames(t *testing.T) {
	t.Parallel()

	feature := Feature{Id: "test.feature"}
	for _, name := range []string{"AZD_ALPHA_ENABLE_TEST.FEATURE", "AZD_ALPHA_ENABLE_TEST_FEATURE"} {
		require.True(t, feature.IsEnabledWithSources(nil, map[string]string{name: "true"}))
	}
	require.True(t, feature.IsEnabledWithSources(nil, map[string]string{
		"AZD_ALPHA_ENABLE_TEST.FEATURE": "false",
		"AZD_ALPHA_ENABLE_TEST_FEATURE": "true",
	}))
}

func TestFeature_IsEnabledWithSources_NoAmbientState(t *testing.T) {
	feature := Feature{Id: "test.extension.ambient", EnvVar: "AZD_FOUNDRY_EXT_ENABLE_TEST_AMBIENT"}
	t.Setenv(feature.EnvVar, "true")
	SetDefaultEnablement(feature.Id, true)
	t.Cleanup(func() { ResetDefaultEnablement(feature.Id) })

	require.False(t, feature.IsEnabledWithSources(nil))

	source := map[string]string{feature.EnvVar: "true"}
	require.True(t, feature.IsEnabledWithSources(nil, source))
	source[feature.EnvVar] = "false"
	require.False(t, feature.IsEnabledWithSources(nil, source))
}

func TestFeature_EnvVarNames(t *testing.T) {
	t.Parallel()

	require.Equal(t,
		[]string{"AZD_ALPHA_ENABLE_TEST.FEATURE", "AZD_ALPHA_ENABLE_TEST_FEATURE"},
		Feature{Id: "test.feature"}.EnvVarNames())
	require.Equal(t,
		[]string{"AZD_TEST_EXT_ENABLE_FEATURE"},
		Feature{Id: "test.feature", EnvVar: "AZD_TEST_EXT_ENABLE_FEATURE"}.EnvVarNames())
}
