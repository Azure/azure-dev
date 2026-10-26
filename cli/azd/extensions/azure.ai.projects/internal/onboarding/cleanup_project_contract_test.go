// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnedCleanupProjectChildBeforeParentContracts(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		for _, async := range []bool{false, true} {
			t.Run(fmt.Sprintf("connection=%d/projectAsync=%t", status, async), func(t *testing.T) {
				f := newCleanupARM(t, true, true)
				f.deploy(t)
				f.projectCreate = true
				project := f.base.resources[strings.ToLower(f.spec.ProjectID)]
				delete(project, "tags")
				endpoint := "https://actual.services.ai.azure.com/api/projects/project"
				cleanupObject(t, project["properties"])["endpoints"] = map[string]any{"AI Foundry API": endpoint}
				f.connectionStatus, f.asyncProject, f.accountProjection = status, async, true
				var messages []string
				require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, func(message string) {
					messages = append(messages, message)
				}))
				connection := f.spec.ProjectID + "/connections/" + f.base.plan.Registry.Name + "-conn"
				connectionIndex, projectIndex, accountIndex := -1, -1, -1
				for index, path := range f.deletes {
					switch {
					case strings.EqualFold(path, connection):
						connectionIndex = index
					case strings.EqualFold(path, f.spec.ProjectID):
						projectIndex = index
					case strings.EqualFold(path, f.spec.AccountID):
						accountIndex = index
					}
				}
				require.GreaterOrEqual(t, connectionIndex, 0)
				require.Greater(t, projectIndex, connectionIndex)
				require.Greater(t, accountIndex, projectIndex)
				assert.NotContains(t, f.deletes, f.spec.AccountID+"/connections/"+f.base.plan.Registry.Name+"-conn")
				assert.Contains(t, strings.Join(messages, "\n"), "Deleting owned Foundry project ")
				state, err := f.base.journal.Load(t.Context())
				require.NoError(t, err)
				assert.True(t, state.Foundry.ConnectionDeleted)
				assert.True(t, state.Foundry.ProjectDeleteStarted)
				assert.True(t, state.Foundry.ProjectDeleted)
				assert.Equal(t, endpoint, state.Foundry.ProjectEndpoint)
			})
		}
	}
}

func TestOwnedCleanupProjectFailuresRetainParentAndDependencies(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*cleanupARM)
		want   string
	}{
		{"connection-delete-denied", func(f *cleanupARM) {
			f.failDelete = f.spec.ProjectID + "/connections/" + f.base.plan.Registry.Name + "-conn"
		}, "AuthorizationFailed"},
		{"connection-still-present", func(f *cleanupARM) { f.keepConnection = true }, "not confirmed"},
		{"project-delete-denied", func(f *cleanupARM) { f.failDelete = f.spec.ProjectID }, "AuthorizationFailed"},
		{"project-still-present", func(f *cleanupARM) { f.keepProject = true }, "not confirmed"},
		{"independent-account-connection", func(f *cleanupARM) {
			f.accountProjection, f.keepProjection = true, true
		}, "remaining connection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newCleanupARM(t, false, false)
			f.deploy(t)
			test.change(f)
			require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), test.want)
			assert.NotContains(t, f.deletes, f.spec.AccountID)
			assert.Contains(t, f.base.resources, strings.ToLower(f.spec.AccountID))
			for _, path := range f.deletes {
				assert.True(t, strings.HasPrefix(strings.ToLower(path), strings.ToLower(f.spec.AccountID)+"/"),
					"all supporting resources and grants must remain")
			}
			state, err := f.base.journal.Load(t.Context())
			require.NoError(t, err)
			assert.False(t, state.CleanupComplete)
			f.failDelete, f.keepProject, f.keepConnection, f.keepProjection = "", false, false, false
			require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
		})
	}
}

func TestOwnedCleanupProjectInventoryFailsBeforeAnyDelete(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*cleanupARM)
	}{
		{"foreign-project-connection", func(f *cleanupARM) {
			id := f.spec.ProjectID + "/connections/customer-owned"
			f.base.resources[strings.ToLower(id)] = map[string]any{"id": id}
		}},
		{"changed-registry-target", func(f *cleanupARM) {
			id := strings.ToLower(f.spec.ProjectID + "/connections/" + f.base.plan.Registry.Name + "-conn")
			cleanupObject(t, f.base.resources[id]["properties"])["target"] = "another.azurecr.io"
		}},
		{"changed-registry-principal", func(f *cleanupARM) {
			id := strings.ToLower(f.spec.ProjectID + "/connections/" + f.base.plan.Registry.Name + "-conn")
			p := cleanupObject(t, f.base.resources[id]["properties"])
			cleanupObject(t, p["credentials"])["clientId"] = "foreign-principal"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			test.change(f)
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			require.Empty(t, f.deletes)
		})
	}
}

func TestOwnedCleanupProjectInventoryPaginationAndPermission(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(fmt.Sprintf("denied=%t", denied), func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			transport := f.options.Transport
			f.options.Transport = contractARMOptions(func(request *http.Request) (*http.Response, error) {
				if request.Method == http.MethodGet && strings.EqualFold(
					request.URL.Path, f.spec.ProjectID+"/connections") {
					if request.URL.Query().Get("page") == "2" {
						if denied {
							return contractARMResponse(request, 403, `{"error":{"code":"AuthorizationFailed"}}`), nil
						}
						raw, err := json.Marshal(map[string]any{"value": []any{
							map[string]any{"id": f.spec.ProjectID + "/connections/untracked"},
						}})
						require.NoError(t, err)
						return contractARMResponse(request, 200, string(raw)), nil
					}
					response, err := transport.Do(request)
					require.NoError(t, err)
					var page map[string]any
					require.NoError(t, json.NewDecoder(response.Body).Decode(&page))
					require.NoError(t, response.Body.Close())
					page["nextLink"] = request.URL.String() + "&page=2"
					raw, err := json.Marshal(page)
					require.NoError(t, err)
					return contractARMResponse(request, 200, string(raw)), nil
				}
				return transport.Do(request)
			}).Transport
			f.lifecycle = NewLifecycle(contractCredential{}, f.options, f.base.journal)
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			require.Empty(t, f.deletes)
		})
	}
}

func TestOwnedCleanupProjectResumesAfterChildDeletion(t *testing.T) {
	for _, stage := range []string{"connection", "project", "project-journal", "deleting-project"} {
		t.Run(stage, func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			connection := f.spec.ProjectID + "/connections/" + f.base.plan.Registry.Name + "-conn"
			switch stage {
			case "connection":
				f.failDelete = f.spec.ProjectID
			case "project-journal":
				f.failJournalOnDelete = f.spec.ProjectID
			case "deleting-project":
				f.keepProject = true
			default:
				f.failDelete = f.spec.AccountID
			}
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			assert.NotContains(t, f.deletes, f.spec.AccountID)
			f.failDelete, f.failJournalOnDelete = "", ""
			f.base.journal.fail = false
			if stage == "deleting-project" {
				cleanupObject(t, f.base.resources[strings.ToLower(f.spec.ProjectID)]["properties"])["provisioningState"] = "Deleting"
			}
			f.keepProject = false
			before := len(f.deletes)
			require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			assert.NotContains(t, f.deletes[before:], connection)
		})
	}
}

func TestOwnedCleanupProjectRejectsRecreatedChildren(t *testing.T) {
	for _, child := range []string{"connection", "project"} {
		t.Run(child, func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			id := f.spec.ProjectID
			f.failDelete = f.spec.AccountID
			if child == "connection" {
				f.failDelete = f.spec.ProjectID
				id += "/connections/" + f.base.plan.Registry.Name + "-conn"
			}
			original := f.base.resources[strings.ToLower(id)]
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			f.base.resources[strings.ToLower(id)] = original
			f.failDelete = ""
			before := len(f.deletes)
			require.ErrorContains(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil), "recreated")
			require.Len(t, f.deletes, before)
		})
	}
}

func TestOwnedCleanupProjectCancellationRetainsAccount(t *testing.T) {
	f := newCleanupARM(t, true, true)
	f.deploy(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.cancel, f.cancelOnDelete = cancel, 2
	require.ErrorIs(t, f.lifecycle.Cleanup(ctx, f.base.plan, false, nil), context.Canceled)
	assert.NotContains(t, f.deletes, f.spec.AccountID)
	f.cancel = nil
	require.NoError(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
}

func TestOwnedCleanupAccountPrivateEndpointInventoryFailClosed(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"value":[],"nextLink":"https://management.azure.com/another-page"}`,
		`{"value":[{"id":"/unrelated","properties":{"privateEndpoint":{"id":"/unrelated"}}}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			transport := f.options.Transport
			f.options.Transport = contractARMOptions(func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(strings.ToLower(request.URL.Path), "/privateendpointconnections") {
					return contractARMResponse(request, 200, body), nil
				}
				return transport.Do(request)
			}).Transport
			f.lifecycle = NewLifecycle(contractCredential{}, f.options, f.base.journal)
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			assert.Empty(t, f.deletes)
		})
	}
}

func TestOwnedCleanupAccountConnectionRequiresCurrentExactIdentity(t *testing.T) {
	for _, scenario := range []string{"forbidden", "not-found", "empty", "wrong-principal", "wrong-registry", "user-assigned"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCleanupARM(t, true, true)
			f.deploy(t)
			f.accountProjection = true
			transport := f.options.Transport
			f.options.Transport = contractARMOptions(func(request *http.Request) (*http.Response, error) {
				expected := f.spec.AccountID + "/connections/" + f.base.plan.Registry.Name + "-conn/listSecrets"
				if !strings.EqualFold(request.URL.Path, expected) {
					return transport.Do(request)
				}
				if scenario == "forbidden" || scenario == "not-found" {
					status := http.StatusForbidden
					if scenario == "not-found" {
						status = http.StatusNotFound
					}
					return contractARMResponse(request, status, `{"error":{"code":"CurrentIdentityUnavailable"}}`), nil
				}
				if scenario == "empty" {
					return contractARMResponse(request, 200, `{}`), nil
				}
				response, err := transport.Do(request)
				require.NoError(t, err)
				var actual map[string]any
				require.NoError(t, json.NewDecoder(response.Body).Decode(&actual))
				require.NoError(t, response.Body.Close())
				properties := cleanupObject(t, actual["properties"])
				switch scenario {
				case "wrong-principal":
					cleanupObject(t, properties["credentials"])["clientId"] = "foreign"
				case "wrong-registry":
					cleanupObject(t, properties["credentials"])["resourceId"] = f.spec.RegistryID + "-other"
				case "user-assigned":
					actual["identity"] = map[string]any{"type": "UserAssigned"}
				}
				raw, err := json.Marshal(actual)
				require.NoError(t, err)
				return contractARMResponse(request, 200, string(raw)), nil
			}).Transport
			f.lifecycle = NewLifecycle(contractCredential{}, f.options, f.base.journal)
			require.Error(t, f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil))
			assert.Empty(t, f.deletes)
		})
	}
}

func TestOwnedCleanupRejectsCredentialedProjectEndpointWithoutDisclosure(t *testing.T) {
	f := newCleanupARM(t, true, true)
	f.deploy(t)
	project := f.base.resources[strings.ToLower(f.spec.ProjectID)]
	cleanupObject(t, project["properties"])["endpoints"] = map[string]any{ //nolint:gosec // Synthetic non-disclosure fixture.
		"AI Foundry API": "https://user:private-password@account.services.ai.azure.com/api/projects/project?sig=private-signature",
	}
	err := f.lifecycle.Cleanup(t.Context(), f.base.plan, false, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "private-password")
	assert.NotContains(t, err.Error(), "private-signature")
	assert.Empty(t, f.deletes)
}
