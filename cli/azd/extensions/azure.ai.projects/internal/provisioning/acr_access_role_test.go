// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestACRPendingRoleTransitions(t *testing.T) {
	for _, abac := range []bool{false, true} {
		for _, assigned := range []bool{false, true} {
			for _, administrator := range []bool{false, true} {
				t.Run(fmt.Sprintf("ABAC=%t/assigned=%t/admin=%t", abac, assigned, administrator), func(t *testing.T) {
					actualAssigned := assigned
					p, env := acrTestProvider(t)
					p.acrBinding = acrTestBinding()
					p.acrPending = acrTestPending()
					p.acrPending.PrincipalID, p.acrPending.ProjectResourceID = "principal", testACRProject
					newRole, oldRole := acrPullRole, acrRepositoryReader
					mode := "LegacyRegistryPermissions"
					if abac {
						newRole, oldRole = oldRole, newRole
						mode = "AbacRepositoryPermissions"
					}
					p.acrPending.RoleDefinitionID = "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/" + oldRole
					old := *p.acrPending
					data, err := json.Marshal(old)
					require.NoError(t, err)
					env.config = map[string][]byte{acrPendingConfigKey: data}
					grant := acrTestAssignment()
					grant.Properties.RoleDefinitionID = old.RoleDefinitionID
					admin := acrTestAssignment()
					admin.ID = testACRID + "/providers/Microsoft.Authorization/roleAssignments/administrator"
					admin.Properties.Description = ""
					admin.Properties.RoleDefinitionID = "/roles/" + newRole
					var deletes []string
					p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
						if method == http.MethodDelete {
							deletes = append(deletes, path)
							if path == grant.ID {
								actualAssigned = false
							}
							return nil
						}
						require.Equal(t, http.MethodGet, method)
						switch path {
						case testACRID:
							return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
						case testACRProject:
							return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
						case grant.ID:
							if actualAssigned {
								return acrTestReply(out, grant)
							}
							return &azcore.ResponseError{StatusCode: http.StatusNotFound}
						default:
							if strings.Contains(path, "roleAssignments?") {
								var grants []acrRoleAssignment
								if actualAssigned {
									grants = append(grants, grant)
								}
								if administrator {
									grants = append(grants, admin)
								}
								return acrTestReply(out, map[string]any{"value": grants})
							}
							if slices.Contains(old.DeploymentIDs, path) {
								return acrTestReply(out, map[string]any{"properties": map[string]any{
									"provisioningState": "Failed",
									"parameters":        map[string]any{"accessOperationId": map[string]any{"value": old.Token}},
								}})
							}
							require.Contains(t, path, "/Microsoft.Resources/deployments/")
							return &azcore.ResponseError{StatusCode: http.StatusNotFound}
						}
					}
					src := acrTestSource()
					plan, err := p.planACRAccess(t.Context(), src)
					require.NoError(t, err)
					assert.Equal(t, administrator, plan.pullAssigned)
					assert.Empty(t, parameterValue(src, "acrAccessOperationId"))
					assert.Equal(t, old, *p.acrPending)
					assert.Equal(t, data, env.config[acrPendingConfigKey])
					assert.Empty(t, deletes)

					require.NoError(t, p.prepareACRAccess(t.Context(), src))
					assert.NotContains(t, deletes, admin.ID)
					for _, id := range old.DeploymentIDs {
						assert.Contains(t, deletes, id)
					}
					if administrator {
						assert.Nil(t, p.acrPending)
						assert.NotContains(t, env.config, acrPendingConfigKey)
						assert.Equal(t, true, parameterValue(src, "acrPullAssigned"))
						return
					}
					require.NotNil(t, p.acrPending)
					assert.NotEqual(t, old.Token, p.acrPending.Token)
					assert.True(t, strings.HasSuffix(p.acrPending.RoleDefinitionID, newRole))
					assert.Equal(t, p.acrPending.RoleDefinitionID, parameterValue(src, "acrPullRoleDefinitionId"))
					assert.Equal(t, p.acrPending.Token, parameterValue(src, "acrAccessOperationId"))
					reloaded := &FoundryProvisioningProvider{azdClient: p.azdClient, envName: p.envName}
					require.NoError(t, reloaded.loadACRLifecycle(t.Context()))
					assert.Equal(t, p.acrPending, reloaded.acrPending)
				})
			}
		}
	}
}

func TestACRRoleChangeRetainsProofForActiveOrUnavailableDeployment(t *testing.T) {
	for _, state := range []string{"", "Running", "Accepted", "unavailable", "canceled context"} {
		t.Run(state, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.acrBinding, p.acrPending = acrTestBinding(), acrTestPending()
			p.acrPending.PrincipalID = "principal"
			before := *p.acrPending
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				switch path {
				case testACRID:
					return acrTestReply(out, map[string]any{"properties": map[string]any{
						"roleAssignmentMode": "AbacRepositoryPermissions",
					}})
				case testACRProject:
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				case p.acrPending.assignmentID():
					return &azcore.ResponseError{StatusCode: http.StatusNotFound}
				default:
					if strings.Contains(path, "roleAssignments?") {
						return acrTestReply(out, map[string]any{"value": []any{}})
					}
					if state == "unavailable" {
						return errors.New("deployment unavailable")
					}
					if state == "canceled context" {
						return context.Canceled
					}
					return acrTestReply(out, map[string]any{"properties": map[string]any{"provisioningState": state}})
				}
			}
			err := p.prepareACRAccess(t.Context(), acrTestSource())
			require.Error(t, err)
			assert.Equal(t, before, *p.acrPending)
			assert.Empty(t, env.config)
			assert.Empty(t, env.set)
		})
	}
}

func TestACRModeChangeDuringDeploymentRetainsExactOwnership(t *testing.T) {
	for _, abac := range []bool{false, true} {
		for _, administrator := range []bool{false, true} {
			t.Run(fmt.Sprintf("ABAC=%t/admin=%t", abac, administrator), func(t *testing.T) {
				p, env := acrTestProvider(t)
				p.projectPath, p.location = t.TempDir(), "eastus2"
				p.credential = acrTestCredential{}
				p.acrBinding = acrTestBinding()
				p.onDiskSource = acrTestSource()
				p.foundryRGOwnerID = "/subscriptions/sub/resourceGroups/foundry"
				mode, role := "LegacyRegistryPermissions", acrPullRole
				changedMode, changedRole := "AbacRepositoryPermissions", acrRepositoryReader
				if abac {
					mode, changedMode = changedMode, mode
					role, changedRole = changedRole, role
				}

				submitted := false
				var grant acrRoleAssignment
				p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					require.Equal(t, http.MethodGet, method)
					switch path {
					case testACRID:
						current := mode
						if submitted {
							current = changedMode
						}
						return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": current}})
					case testACRProject:
						return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
					default:
						if p.acrPending != nil && path == p.acrPending.assignmentID() {
							return acrTestReply(out, grant)
						}
						if strings.Contains(path, "roleAssignments?") {
							var grants []acrRoleAssignment
							if submitted {
								grants = append(grants, grant)
								if administrator {
									admin := grant
									admin.ID += "-administrator"
									admin.Properties.Description = ""
									admin.Properties.RoleDefinitionID = "/roles/" + changedRole
									grants = append(grants, admin)
								}
							}
							return acrTestReply(out, map[string]any{"value": grants})
						}
						require.Contains(t, path, "/Microsoft.Resources/deployments/")
						return &azcore.ResponseError{StatusCode: http.StatusNotFound}
					}
				}
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
					Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
						require.Equal(t, http.MethodPut, request.Method)
						var deployment struct {
							Properties struct {
								Parameters map[string]struct{ Value any }
							}
						}
						require.NoError(t, json.NewDecoder(request.Body).Decode(&deployment))
						require.NotNil(t, p.acrPending)
						proof := deployment.Properties.Parameters
						require.Equal(t, p.acrPending.RoleDefinitionID, proof["acrPullRoleDefinitionId"].Value)
						require.Equal(t, p.acrPending.Token, proof["acrAccessOperationId"].Value)
						require.True(t, strings.HasSuffix(p.acrPending.RoleDefinitionID, role))
						grant = acrTestAssignment()
						grant.ID = p.acrPending.assignmentID()
						grant.Properties.Description = "azd-foundry:" + p.acrPending.Token
						grant.Properties.RoleDefinitionID = p.acrPending.RoleDefinitionID
						submitted = true
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"properties": map[string]any{
							"provisioningState": "Succeeded",
							"outputs": map[string]any{
								"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"type": "string", "value": testACRID},
								"AZURE_AI_PROJECT_ID":                  map[string]any{"type": "string", "value": testACRProject},
							},
						}})
					}),
				}}
				var progress []string
				result, err := p.Deploy(t.Context(), func(message string) { progress = append(progress, message) })
				if administrator {
					require.NoError(t, err)
					require.NotNil(t, result)
				} else {
					require.ErrorContains(t, err, "permission mode may have changed")
					assert.Nil(t, result)
					assert.NotContains(t, progress, "Foundry deployment complete")
				}
				require.True(t, submitted)
				require.NotNil(t, p.acrBinding)
				require.Len(t, p.acrBinding.OwnedAssignments, 1)
				owned := p.acrBinding.OwnedAssignments[0]
				assert.Equal(t, grant.ID, owned.ID)
				assert.Equal(t, grant.Properties.RoleDefinitionID, owned.RoleDefinitionID)
				assert.Nil(t, p.acrPending)
				reloaded := &FoundryProvisioningProvider{azdClient: p.azdClient, envName: p.envName}
				require.NoError(t, reloaded.loadACRLifecycle(t.Context()))
				assert.Equal(t, p.acrBinding, reloaded.acrBinding)
				assert.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey])
				if !administrator {
					src, err := p.resolveTemplate(t.Context(), func(string) {})
					require.NoError(t, err)
					require.NoError(t, p.prepareACRAccess(t.Context(), src))
					require.NotNil(t, p.acrPending)
					assert.True(t, strings.HasSuffix(p.acrPending.RoleDefinitionID, changedRole))
					assert.NotEqual(t, owned.OwnershipToken, p.acrPending.Token)
				}
				var deleted []string
				reloaded.acrRemoteBuild = true
				reloaded.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					if method == http.MethodDelete {
						deleted = append(deleted, path)
						return nil
					}
					require.Equal(t, http.MethodGet, method)
					if path == grant.ID {
						return acrTestReply(out, grant)
					}
					require.Contains(t, path, "/Microsoft.Resources/deployments/")
					return &azcore.ResponseError{StatusCode: http.StatusNotFound}
				}
				require.NoError(t, reloaded.cleanupACRAccess(t.Context()))
				assert.Equal(t, []string{grant.ID}, deleted)
			})
		}
	}
}

func TestACRModeChangeBeforeAssignmentKeepsRecoverableProof(t *testing.T) {
	for _, abac := range []bool{false, true} {
		t.Run(fmt.Sprintf("ABAC=%t", abac), func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.projectPath, p.location = t.TempDir(), "eastus2"
			p.credential = acrTestCredential{}
			p.acrBinding, p.onDiskSource = acrTestBinding(), acrTestSource()
			p.foundryRGOwnerID = "/subscriptions/sub/resourceGroups/foundry"
			p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
				return map[string]*string{"azd-env-name": new("test")}, true, nil
			}
			mode, role := "LegacyRegistryPermissions", acrPullRole
			changedMode, changedRole := "AbacRepositoryPermissions", acrRepositoryReader
			if abac {
				mode, changedMode = changedMode, mode
				role, changedRole = changedRole, role
			}
			var proof *acrPendingOperation
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				switch path {
				case testACRID:
					current := mode
					if proof != nil {
						current = changedMode
					}
					return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": current}})
				case testACRProject:
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				default:
					if strings.Contains(path, "roleAssignments?") {
						return acrTestReply(out, map[string]any{"value": []any{}})
					}
					if proof != nil && path == "/subscriptions/sub/providers/Microsoft.Resources/deployments/"+p.deploymentName() {
						return acrTestReply(out, map[string]any{"properties": map[string]any{
							"provisioningState": "Failed",
							"parameters":        map[string]any{"acrAccessOperationId": map[string]any{"value": proof.Token}},
						}})
					}
					return &azcore.ResponseError{StatusCode: http.StatusNotFound}
				}
			}
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodPut, request.Method)
					require.NotNil(t, p.acrPending)
					copied := *p.acrPending
					proof = &copied
					assert.True(t, strings.HasSuffix(proof.RoleDefinitionID, role))
					return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"properties": map[string]any{
						"provisioningState": "Failed",
						"error":             map[string]any{"code": "DeploymentFailed", "message": "failed before assignment"},
					}})
				}),
			}}
			result, err := p.Deploy(t.Context(), func(string) {})
			require.Error(t, err)
			assert.Nil(t, result)
			require.NotNil(t, proof)
			assert.Equal(t, proof, p.acrPending)
			reloaded := &FoundryProvisioningProvider{azdClient: p.azdClient, envName: p.envName}
			require.NoError(t, reloaded.loadACRLifecycle(t.Context()))
			assert.Equal(t, proof, reloaded.acrPending)
			assert.NotEmpty(t, env.config[acrPendingConfigKey])
			src, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			require.NoError(t, p.prepareACRAccess(t.Context(), src))
			require.NotNil(t, p.acrPending)
			assert.NotEqual(t, proof.Token, p.acrPending.Token)
			assert.True(t, strings.HasSuffix(p.acrPending.RoleDefinitionID, changedRole))
		})
	}
}
