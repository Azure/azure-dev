// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynthesisOwnsOnlyProjectInputs(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, preserve := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%v/preserve=%v", existing, preserve), func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(root, "model.yaml"), []byte(
					"name: model\nmodel: {name: gpt-4o, format: OpenAI, version: '2024-08-06'}\n"+
						"sku: {name: Standard, capacity: 10}\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
					[]byte("kind: hosted\ncodeConfiguration: {runtime: python_3_13, entryPoint: app.py}\n"), 0o600))
				endpoint := ""
				if existing {
					endpoint = "https://existing.services.ai.azure.com/api/projects/project"
				}
				raw := fmt.Appendf(nil, `services:
  project:
    host: azure.ai.project
    endpoint: %q
    deployments:
      - $ref: ./model.yaml
  agent:
    host: azure.ai.agent
    config:
      $ref: ./agent.yaml
  unrelated-project:
    host: azure.ai.project
    deployments: invalid
    connections:
      $ref: ./missing-bundle.yaml
  connection:
    host: azure.ai.connection
    uses: [project]
    name: '  Private Registry  '
    category: RemoteTool
    target: ${UNRESOLVED_TARGET}
    credentials: {keys: {key: secret-must-not-leak}}
    env: {KEY: '${UNRESOLVED_KEY}'}
  malformed-connection:
    host: azure.ai.connection
    condition: {invalid: condition}
    credentials: [invalid]
    $ref: ./missing-connection.yaml
  toolbox:
    host: azure.ai.toolbox
    uses: [project, connection]
    condition: [invalid]
    config:
      $ref: ./missing-toolbox.yaml
`, endpoint)
				in := Input{RawAzureYAML: raw, ServiceName: "project", ProjectRoot: root, PreserveVarRefs: preserve}
				synthesize := Synthesize
				if existing {
					synthesize = SynthesizeExistingProject
				}
				result, err := synthesize(in)
				require.NoError(t, err)
				assert.NotContains(t, result.Parameters, "connections")
				assert.NotContains(t, result.Parameters, "connectionCredentials")
				assert.Equal(t, false, result.Parameters["includeAcr"])
				deployments, ok := result.Parameters["deployments"].([]Deployment)
				require.True(t, ok)
				require.Len(t, deployments, 1)
				assert.Equal(t, "model", deployments[0].Name)
				assert.Equal(t, 10, deployments[0].Sku.Capacity)
				assert.NotContains(t, fmt.Sprint(result.Parameters), "secret-must-not-leak")
			})
		}
	}
}

func TestSynthesisRejectsBundledDeclarations(t *testing.T) {
	for _, field := range []string{"connections", "toolboxes"} {
		for _, existing := range []bool{false, true} {
			for _, location := range []string{"project", "agent", "agent-config", "inline-agent"} {
				t.Run(fmt.Sprintf("%s/existing=%v/%s", field, existing, location), func(t *testing.T) {
					endpoint := ""
					if existing {
						endpoint = "https://existing.services.ai.azure.com/api/projects/project"
					}
					raw := fmt.Sprintf("services:\n  project:\n    host: azure.ai.project\n    endpoint: %q\n", endpoint)
					switch location {
					case "project":
						raw += fmt.Sprintf("    %s: [{name: legacy, $ref: ./missing.yaml}]\n", field)
					case "agent":
						raw += fmt.Sprintf("  agent:\n    host: azure.ai.agent\n    %s: [{name: legacy, tools: []}]\n", field)
					case "agent-config":
						raw += fmt.Sprintf("  agent:\n    host: azure.ai.agent\n    config:\n      %s: [{name: legacy, tools: []}]\n", field)
					case "inline-agent":
						raw += fmt.Sprintf("    agents:\n      - kind: prompt\n        %s: [{name: legacy, tools: []}]\n", field)
					}
					synthesize := Synthesize
					if existing {
						synthesize = SynthesizeExistingProject
					}
					result, err := synthesize(Input{RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: t.TempDir()})
					require.ErrorContains(t, err, "bundled declarations are no longer supported")
					assert.Contains(t, err.Error(), "migrate")
					assert.Contains(t, err.Error(), field)
					assert.Nil(t, result)
				})
			}
		}
	}
}

func TestSynthesisPreservesAgentReferencesAndConditions(t *testing.T) {
	t.Setenv("ENABLE_AGENT", "true")
	for _, enabled := range []string{"true", "false"} {
		t.Run(enabled, func(t *testing.T) {
			result, err := Synthesize(Input{
				RawAzureYAML: []byte("services:\n  project:\n    host: azure.ai.project\n  agent:\n" +
					"    host: azure.ai.agent\n    condition: ${ENABLE_AGENT}\n    kind: hosted\n" +
					"    toolboxes: [external-toolbox, {name: named-toolbox}]\n"),
				ServiceName: "project", Env: map[string]string{"ENABLE_AGENT": enabled},
			})
			require.NoError(t, err)
			assert.Equal(t, enabled == "true", result.Parameters["includeAcr"])
		})
	}
}

func TestTemplatesDoNotOwnDeclaredConnections(t *testing.T) {
	for name, files := range map[string]fs.FS{
		"bicep": TemplatesFS(), "terraform": TerraformTemplatesFS(),
		"existing-terraform": ExistingProjectTerraformTemplatesFS(),
	} {
		t.Run(name, func(t *testing.T) {
			err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
				require.NoError(t, err)
				if entry.IsDir() {
					return nil
				}
				assert.NotEqual(t, "connections.bicep", entry.Name())
				assert.NotEqual(t, "connections.tf", entry.Name())
				data, err := fs.ReadFile(files, path)
				require.NoError(t, err)
				for _, forbidden := range []string{
					"connectionCredentials", "connectionsType", "connectionType", "param connections ",
					`variable "connections"`, `"connections"`, "var.connections", "projectConnections",
					"AZURE_AI_PROJECT_CONNECTION_NAMES", "AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT",
				} {
					assert.NotContains(t, string(data), forbidden, path)
				}
				return nil
			})
			require.NoError(t, err)
		})
	}
	for _, read := range []func() ([]byte, error){ARMTemplate, ExistingProjectARMTemplate} {
		data, err := read()
		require.NoError(t, err)
		assert.Contains(t, string(data), `"category": "ContainerRegistry"`)
		assert.Contains(t, string(data), "AZURE_AI_PROJECT_ACR_CONNECTION_NAME")
		assert.Contains(t, string(data), "FOUNDRY_PROJECT_ENDPOINT")
	}
}
