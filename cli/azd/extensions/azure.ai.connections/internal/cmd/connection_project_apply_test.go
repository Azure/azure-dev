// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestConnectionAddRawRootResources(t *testing.T) {
	tests := []struct {
		name      string
		section   string
		json      bool
		present   bool
		null      bool
		wantError string
	}{
		{name: "YAML omitted"},
		{name: "YAML empty map", section: "resources: {}\n", present: true},
		{name: "YAML implicit null", section: "resources:\n", present: true, null: true},
		{name: "YAML explicit null", section: "resources: null\n", present: true, null: true},
		{name: "JSON omitted", json: true},
		{name: "JSON empty map", section: `"resources":{},`, json: true, present: true},
		{name: "JSON null", section: `"resources":null,`, json: true, present: true, null: true},
		{
			name: "YAML scalar", section: "resources: private-value\n",
			wantError: "project resources must be a mapping",
		},
		{
			name: "YAML list", section: "resources: []\n",
			wantError: "project resources must be a mapping",
		},
		{
			name: "JSON scalar", section: `"resources":false,`, json: true,
			wantError: "project resources must be a mapping",
		},
		{
			name: "JSON list", section: `"resources":[],`, json: true,
			wantError: "project resources must be a mapping",
		},
		{
			name: "YAML null resource", section: "resources:\n  cache:\n",
			wantError: "invalid core configuration",
		},
		{
			name: "YAML explicit null resource", section: "resources:\n  cache: null\n",
			wantError: "invalid core configuration",
		},
		{
			name: "JSON null resource", section: `"resources":{"cache":null},`, json: true,
			wantError: "invalid core configuration",
		},
		{
			name: "YAML scalar resource", section: "resources:\n  cache: private-value\n",
			wantError: "invalid core configuration",
		},
		{
			name: "JSON list resource", section: `"resources":{"cache":[]},`, json: true,
			wantError: "invalid core configuration",
		},
		{
			name: "YAML invalid resource type", section: "resources:\n  cache:\n    type: []\n",
			wantError: "invalid core configuration",
		},
	}
	for _, existing := range []bool{false, true} {
		for _, test := range tests {
			t.Run(map[bool]string{false: "add/", true: "update/"}[existing]+test.name, func(t *testing.T) {
				root := t.TempDir()
				file := writeConnectionAddDefinition(t, root, "connection.yaml",
					"category: Search\ntarget: ${SEARCH_ENDPOINT}\n")
				worker := map[string]any{
					"host": "containerapp", "language": "js",
					"config": map[string]any{"large": int(9007199254740993), "literal": "${KEEP}"},
				}
				services := map[string]any{
					"foundry":    map[string]any{"host": aiProjectServiceHost},
					"worker.api": worker,
				}
				if existing {
					services["search.api"] = map[string]any{
						"host": aiConnectionHost, "uses": []any{"foundry"}, "category": "Search", "target": "old",
					}
				}
				manifest := map[string]any{"name": "test", "services": services}
				wire, err := yaml.Marshal(manifest)
				require.NoError(t, err)
				content := test.section + string(wire)
				if test.json {
					wire, err = json.Marshal(manifest)
					require.NoError(t, err)
					content = "{" + test.section + strings.TrimPrefix(string(wire), "{")
				}
				manifestPath := writeConnectionAddDefinition(t, root, "azure.yaml", content)
				raw, _, err := foundry.DecodeContainedDocument(root, manifestPath)
				require.NoError(t, err)
				client := newConnectionAddProjectClientStub()
				client.rawManifest = raw
				decodedServices, ok := raw["services"].(map[string]any)
				require.True(t, ok)
				client.services = decodedServices
				rawBefore, err := yaml.Marshal(raw)
				require.NoError(t, err)
				diskClient := struct{ connectionAddProjectClient }{client}
				plan, err := planConnectionAdd(t.Context(), diskClient, root, "search.api",
					file, false, "", true, nil, nil)
				if test.wantError != "" {
					requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, test.wantError)
					assert.NotContains(t, err.Error(), "private-value")
					assert.Nil(t, plan)
					assert.Empty(t, client.mutationCalls)
					after, err := yaml.Marshal(raw)
					require.NoError(t, err)
					assert.Equal(t, rawBefore, after)
					// #nosec G304 -- Reads a test fixture.
					diskAfter, err := os.ReadFile(manifestPath)
					require.NoError(t, err)
					assert.Equal(t, []byte(content), diskAfter)
					return
				}
				require.NoError(t, err)
				require.Equal(t, map[bool]string{false: "added", true: "updated"}[existing], plan.mutation)
				assert.Equal(t, "foundry", plan.projectKey)
				snapshot, err := loadConnectionProjectSnapshot(t.Context(), diskClient, root)
				require.NoError(t, err)
				require.NotNil(t, snapshot.resources)
				assert.Empty(t, snapshot.resources)
				resourceValue, present := snapshot.manifest["resources"]
				assert.Equal(t, test.present, present)
				if test.null {
					require.Nil(t, resourceValue)
				} else if test.present {
					assert.Equal(t, map[string]any{}, resourceValue)
				}
				require.NoError(t, plan.apply(t.Context(), client))
				assert.Equal(t, plan.serviceSection, client.services["search.api"])
				assert.Equal(t, worker, client.services["worker.api"])
				for _, request := range client.serviceSetRequests {
					assert.Equal(t, "search.api", request.GetServiceName())
				}
				after, err := client.readAuthoringManifest(t.Context())
				require.NoError(t, err)
				afterResources, present := after["resources"]
				assert.Equal(t, test.present, present)
				assert.Equal(t, resourceValue, afterResources)
				assert.Equal(t, resourceValue, snapshot.manifest["resources"])
				if test.null {
					require.Nil(t, raw["resources"])
				}
				repeat, err := planConnectionAdd(t.Context(), client, root, "search.api",
					file, false, "", true, nil, nil)
				require.NoError(t, err)
				require.Equal(t, "unchanged", repeat.mutation)
				writes := len(client.mutationCalls)
				repeatBefore, err := yaml.Marshal(raw)
				require.NoError(t, err)
				require.NoError(t, repeat.apply(t.Context(), client))
				assert.Len(t, client.mutationCalls, writes)
				repeatAfter, err := yaml.Marshal(raw)
				require.NoError(t, err)
				assert.Equal(t, repeatBefore, repeatAfter)
			})
		}
	}
}

func TestConnectionAddRejectsDanglingDependencyWithRootNullResources(t *testing.T) {
	for _, nullYAML := range []string{"resources:\n", "resources: null\n"} {
		for _, afterPlanning := range []bool{false, true} {
			name := strings.TrimSpace(nullYAML) +
				map[bool]string{false: "/before planning", true: "/after planning"}[afterPlanning]
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				file := writeConnectionAddDefinition(t, root, "connection.yaml",
					"category: Search\ntarget: https://search.example.test\n")
				content := nullYAML + `name: test
services:
  foundry:
    host: azure.ai.project
  search.api:
    host: azure.ai.connection
    uses: [foundry, cache]
    category: Search
    target: old
`
				manifestPath := writeConnectionAddDefinition(t, root, "azure.yaml", content)
				raw, _, err := foundry.DecodeContainedDocument(root, manifestPath)
				require.NoError(t, err)
				require.Nil(t, raw["resources"])
				client := newConnectionAddProjectClientStub()
				client.rawManifest = raw
				services, ok := raw["services"].(map[string]any)
				require.True(t, ok)
				client.services = services
				if afterPlanning {
					raw["resources"] = map[string]any{"cache": map[string]any{"type": "local"}}
				}
				plan, err := planConnectionAdd(t.Context(), client, root, "search.api",
					file, false, "", true, nil, nil)
				if afterPlanning {
					require.NoError(t, err)
					raw["resources"] = nil
				} else {
					assert.Nil(t, plan)
				}
				before, marshalErr := yaml.Marshal(raw)
				require.NoError(t, marshalErr)
				if afterPlanning {
					err = plan.apply(t.Context(), client)
				}
				requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter,
					`dependency "cache" in uses does not exist`)
				assert.Empty(t, client.mutationCalls)
				after, err := yaml.Marshal(raw)
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
	}
}

func TestConnectionAddValidatesCompleteProjectBeforeWriting(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, invalid := range []string{"project hooks", "unrelated service", "selected service"} {
			t.Run(map[bool]string{false: "add", true: "update"}[existing]+"/"+invalid, func(t *testing.T) {
				root := t.TempDir()
				file := writeConnectionAddDefinition(t, root, "connection.yaml",
					"category: Search\ntarget: https://search.example.test\n")
				client := newConnectionAddProjectClientStub()
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				if existing {
					client.services["search"] = map[string]any{
						"host": aiConnectionHost, "uses": []any{"foundry"},
						"category": "Search", "target": "old",
					}
				}
				switch invalid {
				case "project hooks":
					client.manifestFields = map[string]any{
						"hooks": map[string]any{"predeploy": "private-parser-value"},
					}
				case "unrelated service":
					client.services["worker"] = map[string]any{
						"host": "containerapp", "language": map[string]any{"private-parser-value": true},
					}
				case "selected service":
					if !existing {
						client.manifestFields = map[string]any{"infra": "private-parser-value"}
					} else {
						service, ok := client.services["search"].(map[string]any)
						require.True(t, ok)
						service["language"] = map[string]any{"private-parser-value": true}
					}
				}
				before, err := client.readAuthoringManifest(t.Context())
				require.NoError(t, err)
				plan, err := planConnectionAdd(t.Context(), client, root, "search",
					file, false, "", true, nil, nil)
				require.ErrorContains(t, err, "invalid core configuration")
				assert.NotContains(t, err.Error(), "private-parser-value")
				assert.Nil(t, plan)
				assert.Empty(t, client.mutationCalls)
				after, err := client.readAuthoringManifest(t.Context())
				require.NoError(t, err)
				assert.Equal(t, before, after)
			})
		}
	}
}

func TestConnectionAddValidatesCandidateServiceGraph(t *testing.T) {
	for _, test := range []struct {
		name       string
		projectUse string
		workerUse  string
		wantCycle  bool
	}{
		{name: "acyclic owner switch", projectUse: "project-a"},
		{name: "direct owner cycle", projectUse: "search", wantCycle: true},
		{name: "indirect owner cycle", projectUse: "worker", workerUse: "search", wantCycle: true},
		{name: "self cycle", projectUse: "project-b", wantCycle: true},
		{name: "resource dependency", projectUse: "cache"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml",
				"category: Search\ntarget: https://search.example.test\n")
			client := newConnectionAddProjectClientStub()
			client.services["project-a"] = map[string]any{"host": aiProjectServiceHost}
			client.services["project-b"] = map[string]any{
				"host": aiProjectServiceHost, "uses": []any{test.projectUse},
			}
			client.services["search"] = map[string]any{
				"host": aiConnectionHost, "uses": []any{"project-a"},
				"category": "Search", "target": "old",
			}
			if test.workerUse != "" {
				client.services["worker"] = map[string]any{
					"host": "containerapp", "language": "js", "uses": []any{test.workerUse},
				}
			}
			client.resources["cache"] = map[string]any{"type": "local"}
			before, err := client.readAuthoringManifest(t.Context())
			require.NoError(t, err)
			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, true, "project-b", true, nil, nil)
			if test.wantCycle {
				requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, "dependency cycle")
				assert.Nil(t, plan)
				assert.Empty(t, client.mutationCalls)
				after, err := client.readAuthoringManifest(t.Context())
				require.NoError(t, err)
				assert.Equal(t, before, after)
				return
			}
			require.NoError(t, err)
			require.NoError(t, plan.apply(t.Context(), client))
			repeat, err := planConnectionAdd(t.Context(), client, root, "search",
				file, true, "project-b", true, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "unchanged", repeat.mutation)
			calls := len(client.mutationCalls)
			require.NoError(t, repeat.apply(t.Context(), client))
			assert.Len(t, client.mutationCalls, calls)
		})
	}
}

func TestConnectionAddRejectsServiceCycleIntroducedAfterPlanning(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["project-a"] = map[string]any{"host": aiProjectServiceHost}
	client.services["project-b"] = map[string]any{
		"host": aiProjectServiceHost, "uses": []any{"worker"},
	}
	client.services["worker"] = map[string]any{
		"host": "containerapp", "language": "js", "uses": []any{"project-a"},
	}
	client.services["search"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"project-a"},
		"category": "Search", "target": "old",
	}
	plan, err := planConnectionAdd(t.Context(), client, root, "search",
		file, true, "project-b", true, nil, nil)
	require.NoError(t, err)
	client.services["worker"] = map[string]any{
		"host": "containerapp", "language": "js", "uses": []any{"search"},
	}
	before, err := client.readAuthoringManifest(t.Context())
	require.NoError(t, err)
	err = plan.apply(t.Context(), client)
	requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, "dependency cycle")
	assert.Empty(t, client.mutationCalls)
	after, err := client.readAuthoringManifest(t.Context())
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestConnectionAddReportsUncertainAddFailureAndAllowsRerun(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "before save", true: "after save"}[afterWrite], func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml",
				"category: Search\ntarget: https://search.example.test\n")
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			client.failAt = 1
			client.failAfterWrite = afterWrite
			client.writeError = errors.New("transport interrupted")
			err = plan.apply(t.Context(), client)
			require.ErrorIs(t, err, client.writeError)
			assert.Contains(t, err.Error(), "inspect the manifest and rerun")
			assert.Equal(t, []string{"add"}, client.mutationCalls)
			_, exists := client.services["search"]
			assert.Equal(t, afterWrite, exists)

			client.failAt = 0
			retry, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			require.NoError(t, retry.apply(t.Context(), client))
			if afterWrite {
				assert.Equal(t, "unchanged", retry.mutation)
				assert.Equal(t, []string{"add"}, client.mutationCalls)
			} else {
				assert.Equal(t, "added", retry.mutation)
				assert.Equal(t, []string{"add", "add"}, client.mutationCalls)
			}
		})
	}
}

func TestConnectionAddReportsPartialUpdatesAndAllowsRerun(t *testing.T) {
	operations := []string{
		"set:$ref", "set:uses", "set:env",
		"unset:authType", "unset:category", "unset:credentials", "unset:target",
	}
	for index, operation := range operations {
		for _, afterWrite := range []bool{false, true} {
			name := operation + map[bool]string{false: "/before save", true: "/after save"}[afterWrite]
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				file := writeConnectionAddDefinition(t, root, "connection.yaml", `
category: Search
target: ${SEARCH_ENDPOINT}
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
`)
				client := newConnectionAddProjectClientStub()
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				client.services["second"] = map[string]any{"host": aiProjectServiceHost}
				client.services["search.api"] = map[string]any{
					"host": aiConnectionHost, "uses": []any{"foundry"},
					"category": "Search", "target": "old", "authType": "ApiKey",
					"credentials": map[string]any{"key": "private-value"},
					"config":      map[string]any{"large": int(9007199254740993)},
					"env":         map[string]any{"dotted.key": "keep"},
				}
				plan, err := planConnectionAdd(t.Context(), client, root, "search.api",
					file, true, "second", true, nil, nil)
				require.NoError(t, err)
				client.failAt = index + 1
				client.failAfterWrite = afterWrite
				client.writeError = errors.New("transport interrupted")
				err = plan.apply(t.Context(), client)
				require.ErrorIs(t, err, client.writeError)
				assert.Contains(t, err.Error(), "changes may be partially applied")
				assert.Contains(t, err.Error(), "inspect the manifest and rerun")
				assert.NotContains(t, err.Error(), "private-value")
				assert.Equal(t, operations[:index+1], client.mutationCalls)

				client.failAt = 0
				retry, err := planConnectionAdd(t.Context(), client, root, "search.api",
					file, true, "second", true, nil, nil)
				require.NoError(t, err)
				require.NoError(t, retry.apply(t.Context(), client))
				assert.Equal(t, plan.serviceSection, client.services["search.api"])
				completed := index
				if afterWrite {
					completed++
				}
				assert.Equal(t, operations[completed:], client.mutationCalls[index+1:])
				repeat, err := planConnectionAdd(t.Context(), client, root, "search.api",
					file, true, "second", true, nil, nil)
				require.NoError(t, err)
				require.Equal(t, "unchanged", repeat.mutation)
				writes := len(client.mutationCalls)
				require.NoError(t, repeat.apply(t.Context(), client))
				assert.Len(t, client.mutationCalls, writes)
			})
		}
	}
}

func TestConnectionAddWritesLiteralBindingsAfterAddService(t *testing.T) {
	for _, failure := range []string{"none", "before env save", "after env save"} {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("SEARCH_KEY", "private-env-value")
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml", `
category: Search
target: ${SEARCH_ENDPOINT}
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
metadata:
  region: ${OPTIONAL_REGION:-westus}
`)
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			assert.Equal(t, []string{"SEARCH_ENDPOINT", "SEARCH_KEY"}, plan.environmentVariables)
			if failure != "none" {
				client.failAt = 2
				client.failAfterWrite = failure == "after env save"
				client.writeError = errors.New("transport interrupted")
			}
			err = plan.apply(t.Context(), client)
			assert.Equal(t, []string{"add", "set:env"}, client.mutationCalls)
			require.Len(t, client.addRequests, 1)
			assert.Empty(t, client.addRequests[0].GetService().GetEnvironment())
			if failure == "none" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, client.writeError)
				assert.Contains(t, err.Error(), "changes may be partially applied")
				assert.Contains(t, err.Error(), "rerun to reconcile")
				assert.NotContains(t, err.Error(), "private-env-value")
			}
			service, ok := client.services["search"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "./connection.yaml", service["$ref"])
			if failure == "before env save" {
				assert.NotContains(t, service, "env")
			}
			client.failAt = 0
			retry, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			require.NoError(t, retry.apply(t.Context(), client))
			assert.Equal(t, plan.serviceSection, client.services["search"])
			assert.Equal(t, map[string]any{
				"SEARCH_ENDPOINT": "${SEARCH_ENDPOINT}",
				"SEARCH_KEY":      "${SEARCH_KEY}",
				"OPTIONAL_REGION": "${OPTIONAL_REGION}",
			}, service["env"])
			assert.Len(t, client.addRequests, 1)
			if failure == "before env save" {
				assert.Equal(t, []string{"add", "set:env", "set:env"}, client.mutationCalls)
			} else {
				assert.Equal(t, "unchanged", retry.mutation)
				assert.Equal(t, []string{"add", "set:env"}, client.mutationCalls)
			}
		})
	}
}

func TestConnectionAddPreservesTargetExplicitEmptyEnvironment(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search.api"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"foundry"},
		"$ref": "./old.yaml", "env": map[string]any{},
	}
	plan, err := planConnectionAdd(t.Context(), client, root, "search.api",
		file, false, "", true, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "updated", plan.mutation)
	require.NoError(t, plan.apply(t.Context(), client))
	assert.Equal(t, []string{"set:$ref"}, client.mutationCalls)
	assert.Equal(t, plan.serviceSection, client.services["search.api"])
	env, found := plan.serviceSection["env"]
	assert.True(t, found)
	assert.Equal(t, map[string]any{}, env)

	repeat, err := planConnectionAdd(t.Context(), client, root, "search.api",
		file, false, "", true, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "unchanged", repeat.mutation)
	require.NoError(t, repeat.apply(t.Context(), client))
	assert.Equal(t, []string{"set:$ref"}, client.mutationCalls)
}

func TestConnectionAddPreservesUnrelatedExplicitEmptyEnvironment(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, afterPlanning := range []bool{false, true} {
			name := map[bool]string{false: "add", true: "update"}[existing] +
				map[bool]string{false: "/before planning", true: "/after planning"}[afterPlanning]
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				file := writeConnectionAddDefinition(t, root, "connection.yaml",
					"category: Search\ntarget: https://search.example.test\n")
				client := newConnectionAddProjectClientStub()
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				worker := map[string]any{"host": "containerapp", "language": "js"}
				client.services["worker"] = worker
				if !afterPlanning {
					worker["env"] = map[string]any{}
				}
				if existing {
					client.services["search"] = map[string]any{
						"host": aiConnectionHost, "uses": []any{"foundry"},
						"category": "Search", "target": "old",
					}
				}
				plan, err := planConnectionAdd(t.Context(), client, root, "search",
					file, false, "", true, nil, nil)
				require.NoError(t, err)
				if afterPlanning {
					worker["env"] = map[string]any{}
				}
				before, err := yaml.Marshal(worker)
				require.NoError(t, err)
				snapshot, err := loadConnectionProjectSnapshot(t.Context(), client, root)
				require.NoError(t, err)
				service, ok := snapshot.services["worker"].(map[string]any)
				require.True(t, ok)
				env, found := service["env"]
				require.True(t, found, "the YAML round-trip must preserve the explicit env key")
				require.Equal(t, map[string]any{}, env)

				require.NoError(t, plan.apply(t.Context(), client))
				assert.NotEmpty(t, client.mutationCalls)
				assert.Equal(t, plan.serviceSection, client.services["search"])
				after, err := yaml.Marshal(client.services["worker"])
				require.NoError(t, err)
				assert.Equal(t, before, after)

				repeat, err := planConnectionAdd(t.Context(), client, root, "search",
					file, false, "", true, nil, nil)
				require.NoError(t, err)
				require.Equal(t, "unchanged", repeat.mutation)
				writes := len(client.mutationCalls)
				require.NoError(t, repeat.apply(t.Context(), client))
				assert.Len(t, client.mutationCalls, writes)
			})
		}
	}
}

func TestConnectionAddAllowsNonEmptyAndMissingEnvironment(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, includeEnv := range []bool{false, true} {
			name := map[bool]string{false: "add", true: "update"}[existing] +
				map[bool]string{false: "/missing env", true: "/non-empty env"}[includeEnv]
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				file := writeConnectionAddDefinition(t, root, "connection.yaml",
					"category: Search\ntarget: https://search.example.test\n")
				client := newConnectionAddProjectClientStub()
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				worker := map[string]any{"host": "containerapp", "language": "js"}
				client.services["worker"] = worker
				if includeEnv {
					worker["env"] = map[string]any{"KEEP": "worker-value"}
				}
				if existing {
					service := map[string]any{
						"host": aiConnectionHost, "uses": []any{"foundry"},
						"category": "Search", "target": "old",
					}
					if includeEnv {
						service["env"] = map[string]any{"KEEP": "connection-value"}
					}
					client.services["search"] = service
				}
				plan, err := planConnectionAdd(t.Context(), client, root, "search",
					file, false, "", true, nil, nil)
				require.NoError(t, err)
				require.Equal(t, map[bool]string{false: "added", true: "updated"}[existing], plan.mutation)
				require.NoError(t, plan.apply(t.Context(), client))
				assert.NotEmpty(t, client.mutationCalls)
				assert.Equal(t, plan.serviceSection, client.services["search"])
				assert.Equal(t, worker, client.services["worker"])
			})
		}
	}
}

func TestConnectionAddPreservesExplicitNullEnvironment(t *testing.T) {
	for _, envYAML := range []string{"env: null", "env:"} {
		for _, target := range []bool{false, true} {
			name := envYAML + map[bool]string{false: "/unrelated service", true: "/target service"}[target]
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				file := writeConnectionAddDefinition(t, root, "connection.yaml",
					"category: Search\ntarget: https://search.example.test\n")
				client := newConnectionAddProjectClientStub()
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				key := "worker"
				serviceYAML := "host: containerapp\nlanguage: js\n"
				if target {
					key = "search"
					serviceYAML = "host: azure.ai.connection\nuses: [foundry]\ncategory: Search\ntarget: old\n"
				}
				var service map[string]any
				require.NoError(t, yaml.Unmarshal([]byte(serviceYAML+envYAML+"\n"), &service))
				env, found := service["env"]
				require.True(t, found)
				require.Nil(t, env)
				client.services[key] = service
				plan, err := planConnectionAdd(t.Context(), client, root, "search",
					file, false, "", true, nil, nil)
				require.NoError(t, err)
				before, err := yaml.Marshal(service["env"])
				require.NoError(t, err)

				require.NoError(t, plan.apply(t.Context(), client))
				assert.NotEmpty(t, client.mutationCalls)
				assert.Equal(t, plan.serviceSection, client.services["search"])
				persisted, ok := client.services[key].(map[string]any)
				require.True(t, ok)
				env, found = persisted["env"]
				require.True(t, found)
				assert.Nil(t, env)
				after, err := yaml.Marshal(env)
				require.NoError(t, err)
				assert.Equal(t, before, after)
				for _, request := range client.serviceSetRequests {
					assert.NotEqual(t, "env", request.GetPath())
				}

				repeat, err := planConnectionAdd(t.Context(), client, root, "search",
					file, false, "", true, nil, nil)
				require.NoError(t, err)
				require.Equal(t, "unchanged", repeat.mutation)
				writes := len(client.mutationCalls)
				require.NoError(t, repeat.apply(t.Context(), client))
				assert.Len(t, client.mutationCalls, writes)
			})
		}
	}
}

func TestConnectionAddAllowsUnchangedExplicitNullEnvironment(t *testing.T) {
	for _, envYAML := range []string{"env: null", "env:"} {
		t.Run(envYAML, func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml",
				"category: Search\ntarget: https://search.example.test\n")
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			var service map[string]any
			serviceYAML := "host: azure.ai.connection\nuses: [foundry]\n$ref: ./connection.yaml\n"
			require.NoError(t, yaml.Unmarshal([]byte(serviceYAML+envYAML+"\n"), &service))
			client.services["search"] = service
			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "unchanged", plan.mutation)
			before, err := client.readAuthoringManifest(t.Context())
			require.NoError(t, err)

			require.NoError(t, plan.apply(t.Context(), client))
			assert.Empty(t, client.mutationCalls)
			after, err := client.readAuthoringManifest(t.Context())
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestConnectionAddAllowsUnchangedExplicitEmptyEnvironment(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"foundry"},
		"$ref": "./connection.yaml", "env": map[string]any{},
	}
	plan, err := planConnectionAdd(t.Context(), client, root, "search",
		file, false, "", true, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "unchanged", plan.mutation)
	require.NoError(t, plan.apply(t.Context(), client))
	assert.Empty(t, client.mutationCalls)
	assert.Equal(t, plan.serviceSection, client.services["search"])
}

func TestConnectionAddRejectsProjectChangedAfterPlanning(t *testing.T) {
	tests := map[string]func(*connectionAddProjectClientStub){
		"same key added": func(c *connectionAddProjectClientStub) {
			c.services["search"] = map[string]any{"host": "containerapp", "language": "js"}
		},
		"project host changed": func(c *connectionAddProjectClientStub) {
			c.services["foundry"] = map[string]any{"host": "containerapp"}
		},
		"project removed": func(c *connectionAddProjectClientStub) {
			delete(c.services, "foundry")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml",
				"category: Search\ntarget: https://search.example.test\n")
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)

			mutate(client)
			before, err := client.readAuthoringManifest(t.Context())
			require.NoError(t, err)

			err = plan.apply(t.Context(), client)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "project manifest changed after")
			assert.Contains(t, err.Error(), "Rerun the command")
			assert.Empty(t, client.mutationCalls)
			after, err := client.readAuthoringManifest(t.Context())
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestConnectionAddRejectsExistingServiceChangedAfterPlanning(t *testing.T) {
	for _, test := range []struct {
		name  string
		field string
		value any
	}{
		{"host", "host", "containerapp"},
		{"uses", "uses", []any{"other"}},
		{"empty env to null", "env", nil},
		{"env binding", "env", map[string]any{"KEEP": "private-binding"}},
		{"config", "config", map[string]any{"keep": "private-config"}},
		{"credentials", "credentials", map[string]any{"key": "private-key"}},
		{"inline definition", "target", "https://other.example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml",
				"category: Search\ntarget: https://search.example.test\n")
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			service := map[string]any{
				"host": aiConnectionHost, "uses": []any{"foundry"}, "category": "Search", "target": "old",
				"env": map[string]any{}, "config": map[string]any{"keep": "old"},
			}
			client.services["search"] = service
			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "updated", plan.mutation)

			service[test.field] = test.value
			before, err := yaml.Marshal(client.services)
			require.NoError(t, err)
			err = plan.apply(t.Context(), client)
			localErr := requireConnectionValidationError(t, err, exterrors.CodeProjectChanged, "project manifest changed")
			assert.Contains(t, localErr.Suggestion, "Rerun the command")
			assert.Contains(t, localErr.Message, "no changes were written")
			assert.NotContains(t, err.Error(), "private-")
			assert.Empty(t, client.mutationCalls)
			after, err := yaml.Marshal(client.services)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestConnectionAddRejectsResourceRemovedAfterPlanning(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.resources["cache"] = map[string]any{"type": "local"}
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"foundry", "cache"}, "category": "Search", "target": "old",
	}
	plan, err := planConnectionAdd(t.Context(), client, root, "search",
		file, false, "", true, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "updated", plan.mutation)
	delete(client.resources, "cache")
	before, err := client.readAuthoringManifest(t.Context())
	require.NoError(t, err)

	err = plan.apply(t.Context(), client)
	requireConnectionValidationError(t, err, exterrors.CodeInvalidParameter, `dependency "cache" in uses does not exist`)
	assert.Empty(t, client.mutationCalls)
	after, err := client.readAuthoringManifest(t.Context())
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestConnectionAddAllowsUnrelatedFieldChangesAfterPlanning(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	plan, err := planConnectionAdd(t.Context(), client, root, "search",
		file, false, "", true, nil, nil)
	require.NoError(t, err)

	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost, "config": map[string]any{"a": "b"}}
	client.services["worker"] = map[string]any{"host": "containerapp"}
	require.NoError(t, plan.apply(t.Context(), client))
	assert.Equal(t, []string{"add"}, client.mutationCalls)
}

func TestConnectionAddUpdatesDottedKeyAndRemovesOwnedFields(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "inline", true: "legacy config"}[legacy], func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml",
				"category: Search\ntarget: https://search.example.test\n")
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			client.services["search"] = map[string]any{
				"host": "containerapp", "language": "js",
				"api": map[string]any{
					"category": "keep", "credentials": map[string]any{"key": "keep"},
					"config": map[string]any{"large": uint64(9007199254740993)},
				},
			}
			siblingBefore, err := yaml.Marshal(client.services["search"])
			require.NoError(t, err)
			service := map[string]any{
				"host": aiConnectionHost, "uses": []any{"foundry"},
				"env": map[string]any{"dotted.key": "${LITERAL_VALUE}"},
			}
			oldDefinition := map[string]any{
				"category": "Search", "target": "old", "authType": "ApiKey",
				"credentials": map[string]any{"key": "private-value"},
			}
			if legacy {
				service["config"] = oldDefinition
			} else {
				maps.Copy(service, oldDefinition)
				service["config"] = map[string]any{
					"large": uint64(9007199254740993), "literal": "${LITERAL_VALUE}",
				}
			}
			client.services["search.api"] = service
			plan, err := planConnectionAdd(t.Context(), client, root, "search.api",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "updated", plan.mutation)
			require.NoError(t, plan.apply(t.Context(), client))
			expected, err := yaml.Marshal(plan.serviceSection)
			require.NoError(t, err)
			actual, err := yaml.Marshal(service)
			require.NoError(t, err)
			assert.Equal(t, expected, actual)
			assert.Equal(t, "./connection.yaml", service["$ref"])
			for key := range oldDefinition {
				assert.NotContains(t, service, key)
			}
			if legacy {
				assert.NotContains(t, service, "config")
				assert.Equal(t, []string{"unset:config", "set:$ref"}, client.mutationCalls)
			} else {
				assert.Equal(t, map[string]any{
					"large": uint64(9007199254740993), "literal": "${LITERAL_VALUE}",
				}, service["config"])
				assert.Equal(t, []string{
					"set:$ref", "unset:authType", "unset:category",
					"unset:credentials", "unset:target",
				}, client.mutationCalls)
			}
			assert.Equal(t, map[string]any{"dotted.key": "${LITERAL_VALUE}"}, service["env"])
			for _, request := range client.serviceSetRequests {
				assert.Equal(t, "search.api", request.GetServiceName())
			}
			for _, request := range client.serviceUnsetRequests {
				assert.Equal(t, "search.api", request.GetServiceName())
			}
			siblingAfter, err := yaml.Marshal(client.services["search"])
			require.NoError(t, err)
			assert.Equal(t, siblingBefore, siblingAfter)

			repeat, err := planConnectionAdd(t.Context(), client, root, "search.api",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "unchanged", repeat.mutation)
			writes := len(client.mutationCalls)
			require.NoError(t, repeat.apply(t.Context(), client))
			assert.Len(t, client.mutationCalls, writes)
		})
	}
}

func TestConnectionAddAllowsDottedKeyAddAndSetOnlyUpdate(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	plan, err := planConnectionAdd(t.Context(), client, root, "search.api",
		file, false, "", true, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "added", plan.mutation)
	require.NoError(t, plan.apply(t.Context(), client))

	other := writeConnectionAddDefinition(t, root, "other.yaml",
		"category: Search\ntarget: https://other.example.test\n")
	update, err := planConnectionAdd(t.Context(), client, root, "search.api",
		other, false, "", true, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "updated", update.mutation)
	require.NoError(t, update.apply(t.Context(), client))
	service, ok := client.services["search.api"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "./other.yaml", service["$ref"])
}

func TestConnectionAddLegacyConfigMigrationRecoversFromInterruption(t *testing.T) {
	for _, afterWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "before save", true: "after save"}[afterWrite], func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml",
				"category: Search\ntarget: https://search.example.test\n")
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			client.services["search.api"] = map[string]any{
				"host": aiConnectionHost, "uses": []any{"foundry"},
				"config": map[string]any{
					"category": "Old", "target": "old-target",
					"authType": "ApiKey", "credentials": map[string]any{"key": "private-value"},
				},
			}
			plan, err := planConnectionAdd(t.Context(), client, root, "search.api",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			// Interrupt the write after legacy config removal.
			client.failAt = 1
			if !afterWrite {
				client.failAt = 2
			}
			client.failAfterWrite = afterWrite
			client.writeError = errors.New("transport interrupted")
			err = plan.apply(t.Context(), client)
			require.ErrorIs(t, err, client.writeError)
			assert.Equal(t, "unset:config", client.mutationCalls[0])
			service, ok := client.services["search.api"].(map[string]any)
			require.True(t, ok)
			assert.NotContains(t, service, "config")
			assert.NotContains(t, service, "$ref")

			client.failAt = 0
			retry, err := planConnectionAdd(t.Context(), client, root, "search.api",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "updated", retry.mutation)
			require.NoError(t, retry.apply(t.Context(), client))
			service, ok = client.services["search.api"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "./connection.yaml", service["$ref"])
			assert.NotContains(t, service, "config")
		})
	}
}

func TestConnectionAddRejectsDuplicateNameAddedAfterPlanning(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"name: shared\ncategory: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	plan, err := planConnectionAdd(t.Context(), client, root, "search",
		file, false, "", true, nil, nil)
	require.NoError(t, err)

	client.services["other"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"foundry"},
		"name": "Shared", "category": "Search", "target": "https://other.example.test",
	}
	err = plan.apply(t.Context(), client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve to the same Connection name")
	assert.Empty(t, client.mutationCalls)
	assert.NotContains(t, client.services, "search")
}

func TestConnectionAddRejectsDefinitionChangesAfterPlanning(t *testing.T) {
	const original = `name: original-name
category: Search
target: ${SEARCH_ENDPOINT}
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
metadata:
  region: ${OPTIONAL_REGION:-westus}
`
	outside := t.TempDir()
	outsideFile := writeConnectionAddDefinition(t, outside, "private-outside.yaml", original)
	outsideRef, err := yaml.Marshal(map[string]any{"$ref": outsideFile})
	require.NoError(t, err)
	for _, existing := range []bool{false, true} {
		for _, nested := range []bool{false, true} {
			for _, test := range []struct {
				name    string
				content string
				remove  bool
			}{
				{"effective name conflict", strings.Replace(original, "original-name", "private-conflict", 1), false},
				{"required binding", strings.Replace(original, "SEARCH_KEY", "NEW_KEY", 1), false},
				{"optional binding", strings.Replace(original, "OPTIONAL_REGION", "NEW_REGION", 1), false},
				{"required to optional", strings.Replace(original, "${SEARCH_KEY}", "${SEARCH_KEY:-private-fallback}", 1), false},
				{"optional to required", strings.Replace(original, "${OPTIONAL_REGION:-westus}", "${OPTIONAL_REGION}", 1), false},
				{"optional default", strings.Replace(original, "westus", "eastus", 1), false},
				{"invalid authentication", strings.Replace(original, "ApiKey", "private-auth", 1), false},
				{"core-owned field", original + "env: {}\n", false},
				{"invalid shape", "- private-content\n", false},
				{"malformed document", "credentials: [private-content\n", false},
				{"outside ref", string(outsideRef), false},
				{"credential URL", "$ref: https://private-user:private-password@example.test/file?sig=private-token\n", false},
				{"missing file", "", true},
			} {
				name := map[bool]string{false: "add", true: "update"}[existing] +
					map[bool]string{false: "/root/", true: "/nested/"}[nested] + test.name
				t.Run(name, func(t *testing.T) {
					root := t.TempDir()
					file := writeConnectionAddDefinition(t, root, "connections/search.yaml", original)
					changedFile := file
					if nested {
						writeConnectionAddDefinition(t, root, "connections/search.yaml",
							"$ref: ./nested/details.yaml\n")
						changedFile = writeConnectionAddDefinition(t, root, "connections/nested/details.yaml", original)
					}
					client := newConnectionAddProjectClientStub()
					client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
					client.services["other"] = map[string]any{
						"host": aiConnectionHost, "uses": []any{"foundry"},
						"name": "private-conflict", "category": "Search", "target": "https://other.example.test",
					}
					if existing {
						client.services["search"] = map[string]any{
							"host": aiConnectionHost, "uses": []any{"foundry"},
							"category": "Search", "target": "old",
						}
					}
					manifest, err := client.readAuthoringManifest(t.Context())
					require.NoError(t, err)
					before, err := yaml.Marshal(manifest)
					require.NoError(t, err)
					manifestPath := filepath.Join(root, "azure.yaml")
					require.NoError(t, os.WriteFile(manifestPath, before, 0o600))
					diskClient := struct{ connectionAddProjectClient }{client}
					plan, err := planConnectionAdd(t.Context(), diskClient, root, "search",
						file, false, "", true, nil, nil)
					require.NoError(t, err)
					require.Equal(t, []string{"SEARCH_ENDPOINT", "SEARCH_KEY"}, plan.environmentVariables)
					assert.Equal(t, []string{"OPTIONAL_REGION", "SEARCH_ENDPOINT", "SEARCH_KEY"}, plan.environmentBindings)

					if test.remove {
						require.NoError(t, os.Remove(changedFile))
					} else {
						require.NoError(t, os.WriteFile(changedFile, []byte(test.content), 0o600))
					}
					err = plan.apply(t.Context(), client)
					localErr := requireConnectionValidationError(t, err, exterrors.CodeProjectChanged, "definition changed")
					assert.Contains(t, localErr.Message, "no changes were written")
					assert.Contains(t, localErr.Suggestion, "Rerun the command")
					assert.NotContains(t, err.Error(), root)
					assert.NotContains(t, err.Error(), outside)
					assert.NotContains(t, err.Error(), "private-")
					assert.Empty(t, client.mutationCalls)
					assert.Empty(t, client.addRequests)
					assert.Empty(t, client.serviceSetRequests)
					assert.Empty(t, client.serviceUnsetRequests)
					// #nosec G304 -- Reads a test fixture.
					after, err := os.ReadFile(manifestPath)
					require.NoError(t, err)
					assert.Equal(t, before, after)
				})
			}
		}
	}
}

func TestConnectionAddRejectsNestedCredentialsChangedAfterPlanning(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connections/search.yaml", `
category: Search
target: https://search.example.test
authType: ApiKey
credentials:
  $ref: ./credentials/keys.yaml
`)
	writeConnectionAddDefinition(t, root, "connections/credentials/keys.yaml", "key: ${OLD_KEY}\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	plan, err := planConnectionAdd(t.Context(), client, root, "search",
		file, false, "", true, nil, nil)
	require.NoError(t, err)
	before, err := yaml.Marshal(client.services)
	require.NoError(t, err)

	writeConnectionAddDefinition(t, root, "connections/credentials/keys.yaml", "key: ${NEW_KEY}\n")
	err = plan.apply(t.Context(), client)
	localErr := requireConnectionValidationError(t, err, exterrors.CodeProjectChanged, "definition changed")
	assert.Contains(t, localErr.Message, "no changes were written")
	assert.Contains(t, localErr.Suggestion, "Rerun the command")
	assert.Empty(t, client.mutationCalls)
	after, err := yaml.Marshal(client.services)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestConnectionAddRejectsDefinitionSymlinkEscapeAfterPlanning(t *testing.T) {
	const original = "category: Search\ntarget: https://search.example.test\n"
	outsideFile := writeConnectionAddDefinition(t, t.TempDir(), "private-outside.yaml", original)
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "nested"}[nested], func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connections/search.yaml", original)
			changedFile := file
			if nested {
				writeConnectionAddDefinition(t, root, "connections/search.yaml", "$ref: ./details.yaml\n")
				changedFile = writeConnectionAddDefinition(t, root, "connections/details.yaml", original)
			}
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			before, err := yaml.Marshal(client.services)
			require.NoError(t, err)

			require.NoError(t, os.Remove(changedFile))
			if err := os.Symlink(outsideFile, changedFile); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			err = plan.apply(t.Context(), client)
			localErr := requireConnectionValidationError(t, err, exterrors.CodeProjectChanged, "definition changed")
			assert.Contains(t, localErr.Message, "no changes were written")
			assert.Contains(t, localErr.Suggestion, "Rerun the command")
			assert.NotContains(t, err.Error(), outsideFile)
			assert.NotContains(t, err.Error(), root)
			assert.Empty(t, client.mutationCalls)
			after, err := yaml.Marshal(client.services)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}
