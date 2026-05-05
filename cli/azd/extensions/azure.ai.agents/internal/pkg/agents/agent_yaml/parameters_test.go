// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInjectParameterValues_BracesNoSpaces(t *testing.T) {
	t.Parallel()

	template := "model: {{deploymentName}}"
	params := ParameterValues{"deploymentName": "gpt-4o"}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Equal(t, "model: gpt-4o", string(result))
}

func TestInjectParameterValues_BracesWithSpaces(t *testing.T) {
	t.Parallel()

	template := "model: {{ deploymentName }}"
	params := ParameterValues{"deploymentName": "gpt-4o"}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Equal(t, "model: gpt-4o", string(result))
}

func TestInjectParameterValues_BothForms(t *testing.T) {
	t.Parallel()

	template := "first: {{name}}\nsecond: {{ name }}"
	params := ParameterValues{"name": "my-agent"}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Equal(t, "first: my-agent\nsecond: my-agent", string(result))
}

func TestInjectParameterValues_NonStringValues(t *testing.T) {
	t.Parallel()

	template := "count: {{replicas}}\nenabled: {{debug}}"
	params := ParameterValues{
		"replicas": 3,
		"debug":    true,
	}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Contains(t, string(result), "count: 3")
	require.Contains(t, string(result), "enabled: true")
}

func TestInjectParameterValues_MultipleParams(t *testing.T) {
	t.Parallel()

	template := "model: {{model}}\nregion: {{region}}"
	params := ParameterValues{
		"model":  "gpt-4o",
		"region": "eastus",
	}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Contains(t, string(result), "model: gpt-4o")
	require.Contains(t, string(result), "region: eastus")
}

func TestInjectParameterValues_NoParams(t *testing.T) {
	t.Parallel()

	template := "name: my-agent"
	params := ParameterValues{}

	result, err := injectParameterValues(template, params)
	require.NoError(t, err)
	require.Equal(t, "name: my-agent", string(result))
}

func TestInjectParameterValues_EmptyTemplate(t *testing.T) {
	t.Parallel()

	result, err := injectParameterValues("", ParameterValues{"x": "y"})
	require.NoError(t, err)
	require.Equal(t, "", string(result))
}

func TestInjectParameterValuesIntoManifest_RoundTrip(t *testing.T) {
	t.Parallel()

	manifest := &AgentManifest{
		Name: "test-agent",
		Template: ContainerAgent{
			AgentDefinition: AgentDefinition{
				Kind: AgentKindHosted,
				Name: "test-agent",
			},
		},
	}

	// Even with no params to inject, the round-trip should succeed
	result, err := InjectParameterValuesIntoManifest(manifest, ParameterValues{})
	require.NoError(t, err)
	require.Equal(t, "test-agent", result.Name)
}
