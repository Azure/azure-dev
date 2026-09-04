// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestApplyLayerInputMappings(t *testing.T) {
	t.Parallel()

	env := environment.NewWithValues("test", map[string]string{"SHARED": "from-env"})
	options := Options{
		Inputs:     map[string]string{"LOCAL": "SHARED", "PLANNED": "EARLIER"},
		VirtualEnv: map[string]string{"EARLIER": "from-plan"},
	}

	mapped, providerEnv := applyLayerInputMappings(options, env)

	assert.Equal(t, "from-env", providerEnv.Getenv("LOCAL"))
	assert.NotContains(t, mapped.VirtualEnv, "LOCAL")
	assert.Equal(t, "from-plan", mapped.VirtualEnv["PLANNED"])
	assert.NotContains(t, options.VirtualEnv, "LOCAL")
	assert.NotContains(t, env.Dotenv(), "LOCAL")
	require.NoError(t, providerEnv.Config.Set("provider.value", "scoped"))
	_, has := env.Config.Get("provider.value")
	require.False(t, has, "the provider config must not share mutable state with the base environment")
}

func TestLayerEnvironmentManagerFiltersInputAliases(t *testing.T) {
	t.Parallel()

	baseEnv := environment.NewWithValues("test", map[string]string{
		"LOCAL":  "original",
		"SHARED": "mapped",
		"REMOVE": "old",
	})
	providerEnv := environment.NewWithValues("test", map[string]string{
		"LOCAL":  "mapped",
		"SHARED": "mapped",
		"ADDED":  "new",
	})
	inner := &mockenv.MockEnvManager{}
	inner.On("Save", mock.Anything, baseEnv).Return(nil).Once()
	manager := &layerEnvironmentManager{
		Manager:       inner,
		baseEnv:       baseEnv,
		initialValues: map[string]string{"LOCAL": "mapped", "SHARED": "mapped", "REMOVE": "old"},
		initialConfig: config.Clone(baseEnv.Config),
		inputs:        map[string]string{"LOCAL": "SHARED"},
	}
	require.NoError(t, providerEnv.Config.Set("provider.added", "new"))
	require.NoError(t, baseEnv.Config.Set("concurrent", "preserved"))

	require.NoError(t, manager.Save(t.Context(), providerEnv))
	require.Equal(t, "original", baseEnv.Getenv("LOCAL"))
	require.Equal(t, "mapped", baseEnv.Getenv("SHARED"))
	require.Equal(t, "new", baseEnv.Getenv("ADDED"))
	require.NotContains(t, baseEnv.Dotenv(), "REMOVE")
	value, has := baseEnv.Config.GetString("provider.added")
	require.True(t, has)
	require.Equal(t, "new", value)
	value, has = baseEnv.Config.GetString("concurrent")
	require.True(t, has)
	require.Equal(t, "preserved", value)
	inner.AssertExpectations(t)
}

func TestApplyLayerOutputMappings(t *testing.T) {
	t.Parallel()

	outputs := map[string]OutputParameter{
		"PROJECT_ID": {Type: ParameterTypeString, Value: "id"},
		"ENDPOINT":   {Type: ParameterTypeString, Value: "url"},
	}

	mapped, err := applyLayerOutputMappings(outputs, map[string]string{"PROJECT_ID": "FOUNDRY_PROJECT_ID"})

	require.NoError(t, err)
	assert.Equal(t, "id", mapped["FOUNDRY_PROJECT_ID"].Value)
	assert.Equal(t, "url", mapped["ENDPOINT"].Value)
	assert.NotContains(t, mapped, "PROJECT_ID")
	assert.Contains(t, outputs, "PROJECT_ID")
}

func TestApplyLayerOutputMappings_RejectsCollision(t *testing.T) {
	t.Parallel()

	_, err := applyLayerOutputMappings(
		map[string]OutputParameter{"A": {}, "B": {}},
		map[string]string{"A": "SHARED", "B": "SHARED"},
	)

	require.ErrorContains(t, err, "both map to environment key")
}

func TestValidateLayerOutputMappings(t *testing.T) {
	t.Parallel()

	err := validateLayerOutputMappings(
		map[string]OutputParameter{"BACKEND_OUTPUT": {}, "ENDPOINT": {}},
		map[string]string{
			"Z_MISSING_OUTPUT": "SHARED_Z",
			"BACKEND_OUPUT":    "SHARED_BACKEND",
			"A_MISSING_OUTPUT": "SHARED_A",
		},
	)

	require.EqualError(
		t,
		err,
		`output mappings reference unknown provider outputs: `+
			`A_MISSING_OUTPUT, BACKEND_OUPUT, Z_MISSING_OUTPUT; `+
			`available outputs: BACKEND_OUTPUT, ENDPOINT`,
	)
}

func TestValidateLayerOutputMappings_AcceptsKnownOutputs(t *testing.T) {
	t.Parallel()

	err := validateLayerOutputMappings(
		map[string]OutputParameter{"BACKEND_OUTPUT": {}},
		map[string]string{"BACKEND_OUTPUT": "SHARED_BACKEND"},
	)

	require.NoError(t, err)
}

func TestApplyLayerOutputKeyMappings(t *testing.T) {
	t.Parallel()

	mapped := applyLayerOutputKeyMappings(
		[]string{"PROJECT_ID", "ENDPOINT", "PROJECT_ID"},
		map[string]string{"PROJECT_ID": "FOUNDRY_PROJECT_ID"},
	)

	assert.Equal(t, []string{"ENDPOINT", "FOUNDRY_PROJECT_ID"}, mapped)
}

func TestApplyPlannedOutputMappings(t *testing.T) {
	t.Parallel()

	mapped, err := applyPlannedOutputMappings(
		[]PlannedOutput{{Name: "PROJECT_ID"}, {Name: "ENDPOINT"}},
		map[string]string{"PROJECT_ID": "FOUNDRY_PROJECT_ID"},
	)

	require.NoError(t, err)
	assert.Equal(t, []PlannedOutput{{Name: "FOUNDRY_PROJECT_ID"}, {Name: "ENDPOINT"}}, mapped)
}
