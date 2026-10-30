// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestACRBindingSelection(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	for _, mode := range []string{"create", "reuse-connect"} {
		binding := &ACRBinding{
			Version: 1, Mode: mode, RegistryResourceID: id,
			ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/" +
				"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
			PrincipalID: "principal",
		}
		parsed, err := ParseACRBinding(binding)
		require.NoError(t, err)
		assert.Equal(t, binding, parsed)
		require.NoError(t, ValidateACRBinding(parsed, "", ""))
		require.NoError(t, ValidateACRBinding(parsed, mode, ""))
		require.NoError(t, ValidateACRBinding(parsed, mode, strings.ToUpper(id)))
		require.ErrorContains(t, ValidateACRBinding(parsed, mode, id+"other"), "migration is not supported")
		opposite := "create"
		if mode == opposite {
			opposite = "reuse-connect"
		}
		require.ErrorContains(t, ValidateACRBinding(parsed, opposite, id), "migration is not supported")
	}
	require.NoError(t, ValidateACRBinding(nil, "reuse-connect", id))
	for _, raw := range []string{"null", "{}", `{"version":2}`, `{"extra":true}`, "{} {}"} {
		_, err := ParseACRBinding(raw)
		require.Error(t, err, raw)
	}
}

func TestACRBindingSynthesisGuard(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	in := Input{
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n" +
			"  agent:\n    host: azure.ai.agent\n    kind: hosted\n"),
		ServiceName: "project",
		Env: map[string]string{
			"AZD_FOUNDRY_ACR_MODE": "reuse-connect", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": id,
		},
		ACRBinding: &ACRBinding{
			Version: 1, Mode: "reuse-connect", RegistryResourceID: id, PrincipalID: "principal",
			ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/" +
				"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
		},
	}
	_, err := Synthesize(in)
	require.NoError(t, err)
	in.Env["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = id + "other"
	_, err = Synthesize(in)
	require.ErrorContains(t, err, "migration")
	in.Env["AZD_FOUNDRY_ACR_MODE"] = "create"
	_, err = Synthesize(in)
	require.ErrorContains(t, err, "migration")
	in.ACRBinding = nil
	in.Env["AZD_FOUNDRY_ACR_MODE"] = "none"
	mode, err := GreenfieldACRMode(in)
	require.NoError(t, err)
	assert.Equal(t, "reuse-connect", mode)
	in.Env[ACRBindingEnvKey] = "{}"
	_, err = GreenfieldACRMode(in)
	require.ErrorContains(t, err, "invalid deployed ACR binding")
}

func TestResolveGreenfieldACRSelectionUsesBinding(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	for _, mode := range []string{"create", "reuse-connect"} {
		binding := &ACRBinding{
			Version: 1, Mode: mode, RegistryResourceID: id,
			ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/" +
				"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
			PrincipalID: "principal",
		}
		raw, err := json.Marshal(binding)
		require.NoError(t, err)
		for _, source := range []string{"input", "environment"} {
			for _, alias := range []struct {
				name, mode, id string
			}{
				{name: "absent"},
				{name: "mode only", mode: mode},
				{name: "id only", id: id},
				{name: "matching", mode: mode, id: strings.ToUpper(id)},
			} {
				t.Run(mode+"/"+source+"/"+alias.name, func(t *testing.T) {
					in := Input{
						ResourceGroupName: "foundry",
						Env: map[string]string{
							"AZURE_SUBSCRIPTION_ID":                "sub",
							"AZD_FOUNDRY_ACR_MODE":                 alias.mode,
							"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": alias.id,
						},
					}
					if source == "input" {
						in.ACRBinding = binding
					} else {
						in.Env[ACRBindingEnvKey] = string(raw)
					}
					before := maps.Clone(in.Env)
					selectedMode, selectedID, err := ResolveGreenfieldACRSelection(in)
					require.NoError(t, err)
					assert.Equal(t, mode, selectedMode)
					assert.Equal(t, id, selectedID)
					require.NoError(t, ValidateACRSelector(selectedID, "sub", "foundry"))
					err = ValidateGreenfieldACRSelectionForInput(in, selectedID)
					if mode == "reuse-connect" {
						require.NoError(t, err)
					} else {
						require.ErrorContains(t, err, "migration is not supported")
					}
					require.ErrorContains(t, ValidateGreenfieldACRSelectionForInput(in, ""), "full ARM ID")
					require.ErrorContains(t, ValidateGreenfieldACRSelection(in.Env, ""), "full ARM ID")
					assert.Equal(t, before, in.Env)
					after, err := json.Marshal(binding)
					require.NoError(t, err)
					assert.Equal(t, raw, after)
				})
			}
		}
	}
}

func TestResolveGreenfieldACRSelectionRejectsConflictsAndInvalidBindings(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	binding := &ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: id,
		ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/" +
			"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
		PrincipalID: "principal",
	}
	for _, tt := range []struct {
		name, mode, id string
	}{
		{name: "create mode", mode: "create"},
		{name: "none mode", mode: "none"},
		{name: "unknown mode", mode: "unknown"},
		{name: "different registry", id: id + "other"},
		{name: "malformed registry", id: "invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mode, selectedID, err := ResolveGreenfieldACRSelection(Input{
				ACRBinding: binding,
				Env: map[string]string{
					"AZD_FOUNDRY_ACR_MODE":                 tt.mode,
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": tt.id,
				},
			})
			require.ErrorContains(t, err, "migration is not supported")
			assert.Empty(t, mode)
			assert.Empty(t, selectedID)
		})
	}
	t.Run("invalid input binding", func(t *testing.T) {
		invalid := *binding
		invalid.Version = 2
		_, _, err := ResolveGreenfieldACRSelection(Input{ACRBinding: &invalid})
		require.ErrorContains(t, err, "invalid deployed ACR binding")
		require.ErrorContains(t, ValidateACRBinding(&invalid, "", ""), "invalid deployed ACR binding")
	})
	for _, raw := range []string{"null", "{}", `{"version":2}`, `{"extra":true}`, "{} {}"} {
		t.Run("invalid environment binding/"+raw, func(t *testing.T) {
			_, _, err := ResolveGreenfieldACRSelection(Input{Env: map[string]string{ACRBindingEnvKey: raw}})
			require.ErrorContains(t, err, "invalid deployed ACR binding")
		})
	}
}

func TestACRRequirementsBeforeProjectSetup(t *testing.T) {
	for _, tt := range []struct {
		name, service string
		has, needs    bool
	}{
		{"empty", "services: {}", false, false},
		{"project only", "services:\n  project:\n    host: azure.ai.project", false, false},
		{"source", "services:\n  agent:\n    host: azure.ai.agent\n    kind: hosted", true, true},
		{"image", "services:\n  agent:\n    host: azure.ai.agent\n    image: registry/image:v1", true, false},
		{"code", "services:\n  agent:\n    host: azure.ai.agent\n    codeConfiguration: {}", true, false},
		{"prompt", "services:\n  agent:\n    host: azure.ai.agent\n    kind: prompt", true, false},
		{"disabled", "services:\n  agent:\n    host: azure.ai.agent\n    condition: false", false, false},
		{"legacy image", "services:\n  agent:\n    host: azure.ai.agent\n    agents:\n" +
			"      - name: agent\n        image: registry/image:v1", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			has, needs, err := ACRRequirements(Input{RawAzureYAML: []byte(tt.service)})
			require.NoError(t, err)
			assert.Equal(t, tt.has, has)
			assert.Equal(t, tt.needs, needs)
		})
	}
}

func TestGreenfieldACRResourceGroup(t *testing.T) {
	for _, tt := range []struct {
		name, raw, override, want string
		env                       map[string]string
	}{
		{name: "root default", want: "rg-test"},
		{name: "root explicit", env: map[string]string{"AZURE_RESOURCE_GROUP": "root"}, want: "root"},
		{name: "layer ignores root", raw: "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry",
			env: map[string]string{"AZURE_RESOURCE_GROUP": "root"}, want: "rg-test-foundry"},
		{name: "layer explicit", raw: "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry",
			env: map[string]string{"AZURE_FOUNDRY_RESOURCE_GROUP": "custom"}, want: "custom"},
		{name: "inherited layer", raw: "infra:\n  provider: microsoft.foundry\n  layers:\n    - name: foundry",
			want: "rg-test-foundry"},
		{name: "other layer", raw: "infra:\n  layers:\n    - name: app\n      provider: bicep", want: "rg-test"},
		{name: "override", override: "override", want: "override"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{"AZURE_ENV_NAME": "test"}
			maps.Copy(env, tt.env)
			group, err := GreenfieldACRResourceGroup(Input{
				RawAzureYAML: []byte(tt.raw), ResourceGroupName: tt.override, Env: env,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, group)
		})
	}
}

func TestGreenfieldACRSelectionForInput(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/rg-test-foundry/" +
		"providers/Microsoft.ContainerRegistry/registries/registry"
	in := Input{
		RawAzureYAML: []byte("infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry"),
		Env:          map[string]string{"AZURE_ENV_NAME": "test", "AZURE_RESOURCE_GROUP": "other"},
	}
	require.ErrorContains(t, ValidateGreenfieldACRSelectionForInput(in, id), "configure AZURE_SUBSCRIPTION_ID")
	in.Env["AZURE_SUBSCRIPTION_ID"] = "SUB"
	require.ErrorContains(t, ValidateGreenfieldACRSelectionForInput(in, id), "azd down deletes")
	in.Env["AZURE_SUBSCRIPTION_ID"] = "other"
	require.ErrorContains(t, ValidateGreenfieldACRSelectionForInput(in, id), "same subscription")
	in.Env["AZURE_SUBSCRIPTION_ID"] = "SUB"
	in.ResourceGroupName = "another-group"
	require.NoError(t, ValidateGreenfieldACRSelectionForInput(in, id))
	for _, suffix := range []string{"?sig=secret", "#secret", "%3Fsecret", "@secret", "/child", "\\secret"} {
		err := ValidateGreenfieldACRSelectionForInput(in, id+suffix)
		require.ErrorContains(t, err, "full ARM ID")
		assert.NotContains(t, err.Error(), "secret")
	}
}

func TestGreenfieldACRSelectionRejectsAuthoredExistingProject(t *testing.T) {
	const id = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	for _, host := range []string{"azure.ai.project", "azure.ai.agents"} {
		t.Run(host, func(t *testing.T) {
			in := Input{RawAzureYAML: []byte("services:\n  project:\n    host: " + host +
				"\n    endpoint: ${FOUNDRY_PROJECT_ENDPOINT}\n")}
			err := ValidateGreenfieldACRSelectionForInput(in, id)
			require.ErrorContains(t, err, "only supported for a new Foundry project")
			require.ErrorIs(t, err, ErrEndpointBrownfield)
			in.RawAzureYAML = []byte(string(in.RawAzureYAML) + "    condition: false\n")
			require.NoError(t, ValidateGreenfieldACRSelectionForInput(in, id))
		})
	}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), []byte(
		"host: azure.ai.project\nendpoint: https://account.services.ai.azure.com/api/projects/project\n"), 0o600))
	in := Input{
		RawAzureYAML: []byte("services:\n  project:\n    $ref: project.yaml\n"),
		ProjectRoot:  root,
	}
	err := ValidateGreenfieldACRSelectionForInput(in, id)
	require.ErrorContains(t, err, "only supported for a new Foundry project")
	require.ErrorIs(t, err, ErrEndpointBrownfield)
}
