// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
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

func TestACRReuseForEnvironmentOnDiskParameters(t *testing.T) {
	const registryID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	for _, id := range []string{"", registryID} {
		t.Run(id, func(t *testing.T) {
			t.Setenv(ACRReuseEnvVar, "")
			root := t.TempDir()
			infra := filepath.Join(root, "infra")
			require.NoError(t, os.Mkdir(infra, 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(infra, "main.bicep"), nil, 0o600))
			parameters := []byte(`{"parameters":{"existingAcrResourceId":{"value":"` + id + `"}}}`)
			path := filepath.Join(infra, "main.parameters.json")
			require.NoError(t, os.WriteFile(path, parameters, 0o600))
			in := Input{
				ProjectRoot:  root,
				RawAzureYAML: []byte("infra:\n  provider: microsoft.foundry\n"),
				Env:          map[string]string{"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": "foundry"},
			}
			err := RequireACRReuseForEnvironment(t.Context(), nil, "", in)
			if id == "" {
				require.NoError(t, err)
			} else {
				requireACRDisabled(t, err)
			}
			t.Setenv(ACRReuseEnvVar, "true")
			require.NoError(t, RequireACRReuseForEnvironment(t.Context(), nil, "", in))
			//nolint:gosec // Test-owned temporary parameter file.
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, parameters, after)
		})
	}
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
			enabledMode: "create"},
		{name: "prebuilt image without reuse", agent: "    kind: hosted\n    image: registry.example/agent:v1\n" +
			"    docker: {imagePassthrough: true}\n",
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
	in.ACRPending = true
	requireACRDisabled(t, RequireACRReuseForInput(in))
}

func TestACRDemandConsistencyAcrossFeatureStates(t *testing.T) {
	for _, tt := range []struct {
		name, body  string
		legacy, acr bool
	}{
		{"bare image", "image: registry.example/agent:v1\n", false, true},
		{"source without image", "", false, true},
		{"prebuilt", "image: registry.example/agent:v1\n" +
			"docker: {imagePassthrough: true, remoteBuild: true}\n", false, false},
		{"code", "codeConfiguration: {runtime: python}\n", false, false},
		{"connection", "registryConnectionId: private-registry\n", false, false},
		{"legacy prebuilt", "image: registry.example/agent:v1\n", true, false},
		{"legacy without image", "", true, true},
	} {
		for _, style := range []string{"inline", "service", "ref"} {
			for _, enabled := range []string{"false", "true"} {
				t.Run(tt.name+"/"+style+"/"+enabled, func(t *testing.T) {
					t.Setenv(ACRReuseEnvVar, enabled)
					root := t.TempDir()
					body := "kind: hosted\n" + tt.body
					if !strings.Contains(body, "docker:") {
						body += "docker: {remoteBuild: true}\n"
					}
					raw := "services:\n  project:\n    host: azure.ai.project\n"
					switch style {
					case "inline":
						raw += "    agents:\n      - " +
							strings.ReplaceAll(strings.TrimSuffix(body, "\n"), "\n", "\n        ") + "\n"
					case "service":
						raw += "  agent:\n    host: azure.ai.agent\n    " +
							strings.ReplaceAll(body, "\n", "\n    ")
					case "ref":
						raw += "    $ref: project.yaml\n"
						referenced := "agents:\n  - " +
							strings.ReplaceAll(strings.TrimSuffix(body, "\n"), "\n", "\n    ") + "\n"
						require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), []byte(referenced), 0o600))
					}
					values := map[string]string{}
					if tt.legacy {
						values["AZD_AGENT_SKIP_ACR"] = "true"
					}
					in := Input{RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: root, Env: values}
					hasAgents, needed, err := ACRRequirements(in)
					require.NoError(t, err)
					require.True(t, hasAgents)
					require.Equal(t, tt.acr, needed)
					needed, err = RequiresACR(in)
					require.NoError(t, err)
					require.Equal(t, tt.acr, needed)
					remote, err := RequiresRemoteACRBuild(in)
					require.NoError(t, err)
					require.Equal(t, tt.acr, remote)
					result, err := Synthesize(in)
					require.NoError(t, err)
					require.Equal(t, tt.acr, result.Parameters["includeAcr"])
					require.Equal(t, map[bool]string{false: "none", true: "create"}[tt.acr], result.ACRMode)
				})
			}
		}
	}
}

func TestACRLegacyExistingProjectModeBoundary(t *testing.T) {
	t.Setenv(ACRReuseEnvVar, "false")
	const registryID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
	binding := &ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: registryID,
		ProjectResourceID: "/subscriptions/sub/resourceGroups/foundry/providers/" +
			"Microsoft.CognitiveServices/accounts/account/projects/project",
		PrincipalID: "principal",
	}
	encoded, err := json.Marshal(binding)
	require.NoError(t, err)
	for _, project := range []string{"greenfield", "explicit existing", "endpoint", "ref endpoint"} {
		for _, state := range []string{"legacy", "binding", "saved binding", "pending", "invalid binding"} {
			t.Run(project+"/"+state, func(t *testing.T) {
				root := t.TempDir()
				raw := "services:\n  project:\n    host: azure.ai.project\n"
				endpoint := "endpoint: https://account.services.ai.azure.com/api/projects/project\n"
				values := map[string]string{
					"AZD_FOUNDRY_ACR_MODE": "reuse-connect", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registryID,
				}
				switch project {
				case "explicit existing":
					values["USE_EXISTING_AI_PROJECT"] = "true"
				case "endpoint":
					raw += "    " + endpoint
				case "ref endpoint":
					raw += "    $ref: project.yaml\n"
					require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), []byte(endpoint), 0o600))
				}
				in := Input{RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: root, Env: values}
				switch state {
				case "binding":
					in.ACRBinding = binding
				case "saved binding":
					values[ACRBindingEnvKey] = string(encoded)
				case "pending":
					in.ACRPending = true
				case "invalid binding":
					values[ACRBindingEnvKey] = "{"
				}
				before := maps.Clone(values)
				err := RequireACRReuseForInput(in)
				switch {
				case state == "invalid binding":
					require.Error(t, err)
					require.NotContains(t, err.Error(), ACRReuseEnvVar)
				case state != "legacy" || project == "greenfield":
					requireACRDisabled(t, err)
				default:
					require.NoError(t, err)
					if project == "endpoint" || project == "ref endpoint" {
						result, err := SynthesizeExistingProject(in)
						require.NoError(t, err)
						require.NotNil(t, result)
					}
				}
				require.Equal(t, before, values)
			})
		}
	}
}
