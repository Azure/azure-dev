// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
				client.services["search-api"] = map[string]any{
					"host": aiConnectionHost, "uses": []any{"foundry"},
					"category": "Search", "target": "old", "authType": "ApiKey",
					"credentials": map[string]any{"key": "private-value"},
					"config":      map[string]any{"large": int(9007199254740993)},
					"env":         map[string]any{"dotted.key": "keep"},
				}
				plan, err := planConnectionAdd(t.Context(), client, root, "search-api",
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
				retry, err := planConnectionAdd(t.Context(), client, root, "search-api",
					file, true, "second", true, nil, nil)
				require.NoError(t, err)
				require.NoError(t, retry.apply(t.Context(), client))
				assert.Equal(t, plan.serviceSection, client.services["search-api"])
				completed := index
				if afterWrite {
					completed++
				}
				assert.Equal(t, operations[completed:], client.mutationCalls[index+1:])
				repeat, err := planConnectionAdd(t.Context(), client, root, "search-api",
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

func TestConnectionAddLeavesExplicitEmptyEnvironmentUntouched(t *testing.T) {
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
	require.NoError(t, plan.apply(t.Context(), client))
	assert.Equal(t, []string{"set:$ref"}, client.mutationCalls)
	assert.Equal(t, plan.serviceSection, client.services["search.api"])
	env, found := plan.serviceSection["env"]
	assert.True(t, found)
	assert.Equal(t, map[string]any{}, env)
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
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"foundry"}, "category": "Search", "target": "old",
	}
	plan, err := planConnectionAdd(t.Context(), client, root, "search",
		file, false, "", true, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "updated", plan.mutation)

	client.services["search"] = map[string]any{"host": "containerapp", "language": "js"}
	err = plan.apply(t.Context(), client)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Rerun the command")
	assert.Contains(t, err.Error(), "no changes were written")
	assert.Empty(t, client.mutationCalls)
	assert.Equal(t, map[string]any{"host": "containerapp", "language": "js"}, client.services["search"])
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

func TestConnectionAddRejectsDottedKeyUpdateThatRemovesFields(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search.api"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"foundry"},
		"category": "Search", "target": "old", "credentials": map[string]any{"key": "private-value"},
	}
	_, err := planConnectionAdd(t.Context(), client, root, "search.api",
		file, false, "", true, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "contains a dot")
	assert.Contains(t, err.Error(), "no changes were written")
	assert.NotContains(t, err.Error(), "private-value")
	assert.Empty(t, client.mutationCalls)
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
			client.services["search"] = map[string]any{
				"host": aiConnectionHost, "uses": []any{"foundry"},
				"config": map[string]any{
					"category": "Old", "target": "old-target",
					"authType": "ApiKey", "credentials": map[string]any{"key": "private-value"},
				},
			}
			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			// Interrupt the write that follows the legacy config removal.
			client.failAt = 1
			if !afterWrite {
				client.failAt = 2
			}
			client.failAfterWrite = afterWrite
			client.writeError = errors.New("transport interrupted")
			err = plan.apply(t.Context(), client)
			require.ErrorIs(t, err, client.writeError)
			assert.Equal(t, "unset:config", client.mutationCalls[0])
			service, ok := client.services["search"].(map[string]any)
			require.True(t, ok)
			assert.NotContains(t, service, "config")
			assert.NotContains(t, service, "$ref")

			client.failAt = 0
			retry, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", true, nil, nil)
			require.NoError(t, err)
			require.Equal(t, "updated", retry.mutation)
			require.NoError(t, retry.apply(t.Context(), client))
			service, ok = client.services["search"].(map[string]any)
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
