// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"errors"
	"maps"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestACRReuseProcessSwitch(t *testing.T) {
	t.Setenv("AZD_ALPHA_ENABLE_AGENTS_VOICE", "true")
	t.Setenv("AZD_ALPHA_ENABLE_AGENTS_MANAGED_HARNESS", "true")
	for _, value := range []string{"", "false", "0", "on", "yes", " true", "True", "TRUE", "t", "T", "1", "true"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(ACRReuseEnvVar, value)
			enabled := value == "True" || value == "TRUE" || value == "t" ||
				value == "T" || value == "1" || value == "true"
			require.Equal(t, enabled, ACRReuseEnabled())
			err := RequireACRReuse()
			if enabled {
				require.NoError(t, err)
			} else {
				requireACRDisabled(t, err)
			}
		})
	}
}

func requireACRDisabled(t *testing.T, err error) {
	t.Helper()
	local, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, "feature_not_enabled", local.Code)
	require.Equal(t, azdext.LocalErrorCategoryCompatibility, local.Category)
	require.Contains(t, local.Message, ACRReuseEnvVar+"=true")
	require.Contains(t, local.Suggestion, ACRReuseEnvVar+"=true")
}

func TestSynthesizeACRReuseOptIn(t *testing.T) {
	const registryID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	const projectID = "/subscriptions/sub/resourceGroups/foundry/" +
		"providers/Microsoft.CognitiveServices/accounts/account/projects/project"
	binding := &ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: registryID,
		ProjectResourceID: projectID, PrincipalID: "principal",
	}
	snapshot, err := json.Marshal(binding)
	require.NoError(t, err)
	for _, test := range []struct {
		name        string
		values      map[string]string
		binding     *ACRBinding
		pending     bool
		agent       string
		gated       bool
		enabledMode string
	}{
		{name: "automatic create", agent: "    kind: hosted\n", enabledMode: "create"},
		{name: "project only", enabledMode: "none"},
		{name: "code without reuse", agent: "    kind: hosted\n    codeConfiguration: {runtime: python}\n",
			enabledMode: "none"},
		{name: "image without reuse", agent: "    kind: hosted\n    image: registry.example/agent:v1\n",
			enabledMode: "none"},
		{name: "environment selector", values: map[string]string{
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registryID,
		}, agent: "    kind: hosted\n", gated: true, enabledMode: "reuse-connect"},
		{name: "project only pending selector", values: map[string]string{
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registryID,
		}, gated: true, enabledMode: "none"},
		{name: "canonical without aliases", binding: binding,
			agent: "    kind: hosted\n", gated: true, enabledMode: "reuse-connect"},
		{name: "saved canonical without aliases", values: map[string]string{ACRBindingEnvKey: string(snapshot)},
			agent: "    kind: hosted\n", gated: true, enabledMode: "reuse-connect"},
		{name: "unfinished access without aliases", pending: true,
			agent: "    kind: hosted\n", gated: true, enabledMode: "create"},
		{name: "snapshot cannot opt in", values: map[string]string{
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registryID, ACRReuseEnvVar: "true",
		}, agent: "    kind: hosted\n", gated: true, enabledMode: "reuse-connect"},
		{name: "legacy created registry", values: map[string]string{
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": "/subscriptions/sub/resourceGroups/foundry/" +
				"providers/Microsoft.ContainerRegistry/registries/created",
			"AZURE_CONTAINER_REGISTRY_ENDPOINT": "created.azurecr.io",
			"AZURE_AI_PROJECT_ID":               projectID, "AZD_FOUNDRY_ACR_MODE": "create",
		}, agent: "    kind: hosted\n", enabledMode: "create"},
	} {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": "foundry"}
			maps.Copy(values, test.values)
			before := maps.Clone(values)
			raw := "services:\n  project:\n    host: azure.ai.project\n"
			if test.agent != "" {
				raw += "  agent:\n    host: azure.ai.agent\n" + test.agent
			}
			in := Input{RawAzureYAML: []byte(raw), ServiceName: "project",
				Env: values, ACRBinding: test.binding, ACRPending: test.pending}
			t.Setenv(ACRReuseEnvVar, "")
			result, err := Synthesize(in)
			if test.gated {
				requireACRDisabled(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.enabledMode, result.ACRMode)
			}
			require.Equal(t, before, values)
			t.Setenv(ACRReuseEnvVar, "true")
			result, err = Synthesize(in)
			require.NoError(t, err)
			require.Equal(t, test.enabledMode, result.ACRMode)
			require.Equal(t, before, values)
		})
	}
}

func TestACRReuseBindingCannotBecomeBrownfieldWhileDisabled(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "")
	in := Input{
		RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n" +
			"    endpoint: https://account.services.ai.azure.com/api/projects/project\n"),
		ServiceName: "project", Env: map[string]string{"USE_EXISTING_AI_PROJECT": "true"},
		ACRBinding: &ACRBinding{
			Version: 1, Mode: "reuse-connect",
			RegistryResourceID: "/subscriptions/sub/resourceGroups/shared/" +
				"providers/Microsoft.ContainerRegistry/registries/registry",
			ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/" +
				"providers/Microsoft.CognitiveServices/accounts/account/projects/project",
			PrincipalID: "principal",
		},
	}
	_, err := SynthesizeExistingProject(in)
	requireACRDisabled(t, err)
	in.ACRBinding = nil
	in.Env["AZD_FOUNDRY_ACR_MODE"] = "reuse-connect"
	require.NoError(t, RequireACRReuseForInput(in))
}
