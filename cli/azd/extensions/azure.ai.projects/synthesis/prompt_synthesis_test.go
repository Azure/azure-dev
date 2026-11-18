// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSynthesisPromptConnectionReferences(t *testing.T) {
	for _, test := range []struct {
		name, connections string
		wantError         bool
	}{
		{name: "names", connections: "[search, external-connection]"},
		{name: "empty list", connections: "[]"},
		{name: "name object", connections: "[{name: search}]", wantError: true},
		{name: "definition", connections: "[{name: search, target: 'https://search.example'}]", wantError: true},
		{name: "mixed", connections: "[search, {name: other}]", wantError: true},
		{name: "object ref", connections: "[{$ref: ./connection.yaml}]", wantError: true},
		{name: "blank", connections: "['  ']", wantError: true},
		{name: "number", connections: "[42]", wantError: true},
		{name: "boolean", connections: "[true]", wantError: true},
		{name: "scalar", connections: "search", wantError: true},
		{name: "null", connections: "null", wantError: true},
	} {
		for _, shape := range []string{"inline", "config", "inline-agent", "file", "kind-ref"} {
			for _, existing := range []bool{false, true} {
				for _, preserve := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/existing=%v/preserve=%v", test.name, shape, existing, preserve)
					t.Run(name, func(t *testing.T) {
						root := t.TempDir()
						require.NoError(t, os.WriteFile(filepath.Join(root, "connection.yaml"),
							[]byte("name: search\ntarget: https://search.example\n"), 0o600))
						body := fmt.Sprintf("kind: prompt, name: assistant, model: model, "+
							"instructions: help, connections: %s",
							test.connections)
						raw := "services:\n  project:\n    host: azure.ai.project\n" +
							"    deployments: [{name: model, model: {name: gpt-4o}, sku: {name: Standard, capacity: 10}}]\n"
						if existing {
							raw += "    endpoint: https://existing.services.ai.azure.com/api/projects/project\n"
						}
						switch shape {
						case "inline":
							raw += fmt.Sprintf("  agent: {host: azure.ai.agent, %s}\n", body)
						case "config":
							raw += fmt.Sprintf("  agent: {host: azure.ai.agent, config: {%s}}\n", body)
						case "inline-agent":
							raw += fmt.Sprintf("    agents: [{%s}]\n", body)
						case "file":
							require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
								fmt.Appendf(nil, "{%s}\n", body), 0o600))
							raw += "  agent: {host: azure.ai.agent, $ref: ./agent.yaml}\n"
						case "kind-ref":
							require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"),
								[]byte("kind: prompt\nname: assistant\nmodel: model\ninstructions: help\n"), 0o600))
							raw += fmt.Sprintf("  agent: {host: azure.ai.agent, $ref: ./agent.yaml, connections: %s}\n",
								test.connections)
						}
						// These payloads, refs, conditions and credentials belong to other extensions.
						raw += "  search:\n    host: azure.ai.connection\n    condition: [invalid]\n" +
							"    $ref: ./missing-connection.yaml\n    credentials: {key: secret-must-not-leak}\n" +
							"  toolbox: {host: azure.ai.toolbox, $ref: ./missing-toolbox.yaml}\n"
						synthesize := Synthesize
						if existing {
							synthesize = SynthesizeExistingProject
						}
						result, err := synthesize(Input{
							RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: root, PreserveVarRefs: preserve,
						})
						if test.wantError {
							require.ErrorContains(t, err, "bundled declarations are no longer supported")
							assert.Contains(t, err.Error(), "connections")
							assert.Nil(t, result)
							return
						}
						require.NoError(t, err)
						assert.Equal(t, false, result.Parameters["includeAcr"])
						assert.NotContains(t, result.Parameters, "connections")
						assert.NotContains(t, result.Parameters, "connectionCredentials")
						assert.NotContains(t, fmt.Sprint(result.Parameters), "secret-must-not-leak")
						deployments, ok := result.Parameters["deployments"].([]Deployment)
						require.True(t, ok)
						require.Len(t, deployments, 1)
						assert.Equal(t, "model", deployments[0].Name)
						assert.Equal(t, 10, deployments[0].Sku.Capacity)
					})
				}
			}
		}
	}
}

func TestSynthesisConnectionReferenceKindFromFile(t *testing.T) {
	for _, test := range []struct {
		name, definition, overlay string
		wantError                 bool
	}{
		{name: "prompt", definition: "kind: prompt\n"},
		{name: "hosted", definition: "kind: hosted\n", wantError: true},
		{name: "default hosted", definition: "name: agent\n", wantError: true},
		{name: "voice", definition: "kind: prompt-voice\n", wantError: true},
		{name: "prompt overlay", definition: "kind: hosted\n", overlay: "kind: prompt,"},
		{name: "hosted overlay", definition: "kind: prompt\n", overlay: "kind: hosted,", wantError: true},
	} {
		for _, shape := range []string{"inline", "config", "inline-agent"} {
			t.Run(test.name+"/"+shape, func(t *testing.T) {
				root := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(root, "agent.yaml"), []byte(test.definition), 0o600))
				body := test.overlay + " $ref: ./agent.yaml, connections: [search]"
				raw := "services:\n  project:\n    host: azure.ai.project\n"
				switch shape {
				case "inline":
					raw += fmt.Sprintf("  agent: {host: azure.ai.agent, %s}\n", body)
				case "config":
					raw += fmt.Sprintf("  agent: {host: azure.ai.agent, config: {%s}}\n", body)
				case "inline-agent":
					raw += fmt.Sprintf("    agents: [{%s}]\n", body)
				}
				result, err := Synthesize(Input{RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: root})
				if test.wantError {
					require.ErrorContains(t, err, "bundled declarations are no longer supported")
					assert.Nil(t, result)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, false, result.Parameters["includeAcr"])
			})
		}
	}
}

func TestSynthesisPromptConnectionsRespectConditions(t *testing.T) {
	t.Setenv("ENABLE_AGENT", "true")
	for _, test := range []struct {
		name, kind, connections, enabled string
		wantError                        bool
	}{
		{name: "enabled prompt", kind: "prompt", connections: "[search]", enabled: "true"},
		{name: "disabled prompt", kind: "prompt", connections: "[search]", enabled: "false"},
		{name: "disabled hosted legacy", kind: "hosted", connections: "[{name: search}]", enabled: "false"},
		{name: "enabled hosted legacy", kind: "hosted", connections: "[search]", enabled: "true", wantError: true},
		{name: "enabled prompt definition", kind: "prompt", connections: "[{name: search}]",
			enabled: "true", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := fmt.Sprintf(`services:
  project: {host: azure.ai.project}
  a-build: {host: azure.ai.agent, kind: hosted}
  agent:
    host: azure.ai.agent
    condition: ${ENABLE_AGENT}
    kind: %s
    connections: %s
  disabled:
    host: azure.ai.agent
    condition: false
    $ref: ./missing-agent.yaml
`, test.kind, test.connections)
			result, err := Synthesize(Input{
				RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: t.TempDir(),
				Env: map[string]string{"ENABLE_AGENT": test.enabled},
			})
			if test.wantError {
				require.ErrorContains(t, err, "bundled declarations are no longer supported")
				assert.Nil(t, result)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, true, result.Parameters["includeAcr"], "the hosted sibling still needs ACR")
		})
	}
}

func TestSynthesisPromptToolboxesRemainReferenceOnly(t *testing.T) {
	for _, test := range []struct {
		name, toolboxes string
		wantError       bool
	}{
		{name: "references", toolboxes: "[tools, {name: other}]"},
		{name: "definition", toolboxes: "[{name: tools, tools: []}]", wantError: true},
		{name: "referenced definition", toolboxes: "[{$ref: ./toolbox.yaml}]", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "toolbox.yaml"),
				[]byte("name: tools\ntools: []\n"), 0o600))
			raw := fmt.Appendf(nil, "services:\n  project: {host: azure.ai.project}\n"+
				"  agent: {host: azure.ai.agent, kind: prompt, connections: [search], toolboxes: %s}\n", test.toolboxes)
			result, err := Synthesize(Input{RawAzureYAML: raw, ServiceName: "project", ProjectRoot: root})
			if test.wantError {
				require.ErrorContains(t, err, "bundled declarations are no longer supported")
				assert.Contains(t, err.Error(), "toolboxes")
				assert.Nil(t, result)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, false, result.Parameters["includeAcr"])
		})
	}
}

func TestProjectEndpointEnvironmentExpansion(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	for _, test := range []struct {
		name, value, process, want string
		env                        map[string]string
		wantError                  bool
	}{
		{name: "unset", value: "${SYNTHESIS_PROJECT_ENDPOINT}"},
		{name: "process fallback", value: "${SYNTHESIS_PROJECT_ENDPOINT}", process: endpoint, want: endpoint},
		{name: "environment wins", value: "${SYNTHESIS_PROJECT_ENDPOINT}", process: "https://other.example",
			env: map[string]string{"SYNTHESIS_PROJECT_ENDPOINT": endpoint}, want: endpoint},
		{name: "explicit empty wins", value: "${SYNTHESIS_PROJECT_ENDPOINT}", process: endpoint,
			env: map[string]string{"SYNTHESIS_PROJECT_ENDPOINT": ""}},
		{name: "default", value: "${SYNTHESIS_PROJECT_ENDPOINT:-" + endpoint + "}", want: endpoint},
		{name: "trim", value: "  ${SYNTHESIS_PROJECT_ENDPOINT}  ", process: "  " + endpoint + "  ", want: endpoint},
		{name: "malformed", value: "${SYNTHESIS_PROJECT_ENDPOINT", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SYNTHESIS_PROJECT_ENDPOINT", test.process)
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"),
				fmt.Appendf(nil, "host: azure.ai.project\nendpoint: %q\n", test.value), 0o600))
			raw := []byte("services:\n  project: {$ref: ./project.yaml}\n")
			got, err := ProjectEndpoint(raw, "project", root, test.env)
			if test.wantError {
				require.ErrorContains(t, err, "expand endpoint")
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.want, got)
			}
			for _, preserve := range []bool{false, true} {
				result, err := Synthesize(Input{
					RawAzureYAML: raw, ServiceName: "project", ProjectRoot: root, Env: test.env, PreserveVarRefs: preserve,
				})
				switch {
				case test.wantError:
					require.ErrorContains(t, err, "expand endpoint")
				case test.want != "":
					require.ErrorIs(t, err, ErrEndpointBrownfield)
				default:
					require.NoError(t, err)
					require.NotNil(t, result)
				}
			}
		})
	}
}
