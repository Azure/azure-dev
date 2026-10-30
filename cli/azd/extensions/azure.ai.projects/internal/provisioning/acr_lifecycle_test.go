// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testACRID = "/subscriptions/sub/resourceGroups/shared/providers/Microsoft.ContainerRegistry/registries/registry"
const testACRProject = "/subscriptions/sub/resourceGroups/foundry/" +
	"providers/Microsoft.CognitiveServices/accounts/account/projects/project"
const testACRToken = "11111111-2222-4333-8444-555555555555"

func acrTestBinding() *synthesis.ACRBinding {
	return &synthesis.ACRBinding{
		Version: 1, Mode: "reuse-connect", RegistryResourceID: testACRID,
		ProjectResourceID: testACRProject, PrincipalID: "principal",
	}
}

func acrTestPending() *acrPendingOperation {
	return &acrPendingOperation{
		Version: 1, RegistryResourceID: testACRID, Token: testACRToken,
		RoleDefinitionID: "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/" + acrPullRole,
		DeploymentIDs:    acrAccessDeploymentIDs(testACRID, testACRToken),
		ResourceGroupID:  "/subscriptions/sub/resourceGroups/foundry",
		ProjectName:      "project",
	}
}

func acrTestAssignment() acrRoleAssignment {
	var assignment acrRoleAssignment
	assignment.ID = acrTestPending().assignmentID()
	assignment.Properties.PrincipalID = "principal"
	assignment.Properties.RoleDefinitionID = acrTestPending().RoleDefinitionID
	assignment.Properties.Scope = testACRID
	assignment.Properties.Description = "azd-foundry:" + testACRToken
	return assignment
}

func acrTestSource() *templateSource {
	return &templateSource{
		armTemplate: map[string]any{"parameters": map[string]any{
			"acrPullAssigned": map[string]any{}, "acrAccessOperationId": map[string]any{},
		}},
		parameters: map[string]any{
			"includeAcr":            map[string]any{"value": true},
			"existingAcrResourceId": map[string]any{"value": testACRID},
		},
	}
}

func acrTestProvider(t *testing.T) (*FoundryProvisioningProvider, *resolveEnvStubEnvServer) {
	t.Setenv(synthesis.ACRReuseEnvVar, "true")
	t.Helper()
	env := &resolveEnvStubEnvServer{envName: "test"}
	return &FoundryProvisioningProvider{
		azdClient: newResolveEnvTestClient(t, env, &resolveEnvStubPromptServer{}),
		envName:   "test", subID: "sub", rgName: "foundry", foundryName: "project",
	}, env
}

func acrTestReply(out, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func TestACREffectivePullAccess(t *testing.T) {
	for _, tt := range []struct {
		name, role, scope, condition string
		abac, want                   bool
	}{
		{name: "classic direct", role: acrPullRole, scope: testACRID, want: true},
		{name: "classic inherited", role: acrPullRole, scope: "/subscriptions/sub", want: true},
		{name: "management group inherited", role: acrPullRole,
			scope: "/providers/Microsoft.Management/managementGroups/group", want: true},
		{name: "unrelated scope", role: acrPullRole, scope: "/subscriptions/other", want: false},
		{name: "ABAC reader", role: acrRepositoryReader, scope: testACRID, abac: true, want: true},
		{name: "ABAC writer", role: acrRepositoryWriter, scope: testACRID, abac: true, want: true},
		{name: "ABAC contributor", role: acrRepositoryContrib, scope: testACRID, abac: true, want: true},
		{name: "ABAC ignores classic", role: acrPullRole, scope: testACRID, abac: true},
		{name: "conditional not blanket access", role: acrRepositoryReader, scope: testACRID,
			condition: "repository restriction", abac: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			actual := acrTestAssignment()
			actual.Properties.RoleDefinitionID = "/roles/" + tt.role
			actual.Properties.Scope = tt.scope
			actual.Properties.Condition = tt.condition
			assert.Equal(t, tt.want, effectiveACRPull(actual, testACRID, "PRINCIPAL", tt.abac))
			assert.False(t, effectiveACRPull(actual, testACRID, "unrelated", tt.abac))
		})
	}
}

func TestACRPreparePreservesAdministratorGrant(t *testing.T) {
	for _, abac := range []bool{false, true} {
		t.Run(map[bool]string{false: "classic", true: "ABAC"}[abac], func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.acrBinding = acrTestBinding()
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				switch path {
				case testACRID:
					mode := "LegacyRegistryPermissions"
					if abac {
						mode = "AbacRepositoryPermissions"
					}
					return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
				case testACRProject:
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				default:
					require.Contains(t, path, "roleAssignments?")
					grant := acrTestAssignment()
					grant.ID = testACRID + "/providers/Microsoft.Authorization/roleAssignments/administrator-guid"
					grant.Properties.Description = ""
					if abac {
						grant.Properties.RoleDefinitionID = "/roles/" + acrRepositoryReader
					}
					return acrTestReply(out, map[string]any{"value": []acrRoleAssignment{grant}})
				}
			}
			src := acrTestSource()
			require.NoError(t, p.prepareACRAccess(t.Context(), src))
			assert.Equal(t, true, parameterValue(src, "acrPullAssigned"))
			assert.Nil(t, p.acrPending)
			assert.Empty(t, env.config)
			require.NoError(t, p.cleanupACRAccess(t.Context()))
		})
	}
}

func TestACRPreparePersistsIntentBeforeGrant(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodGet, method)
		require.Equal(t, testACRID, path)
		return acrTestReply(out, map[string]any{"properties": map[string]any{
			"roleAssignmentMode": "AbacRepositoryPermissions",
		}})
	}
	src := acrTestSource()
	require.NoError(t, p.prepareACRAccess(t.Context(), src))
	require.NotNil(t, p.acrPending)
	assert.Contains(t, p.acrPending.RoleDefinitionID, acrRepositoryReader)
	assert.NotEmpty(t, env.config[acrPendingConfigKey])
	assert.Equal(t, p.acrPending.Token, parameterValue(src, "acrAccessOperationId"))
	assert.False(t, parameterValue(src, "acrPullAssigned").(bool))
	assert.Nil(t, p.acrBinding)

	p2 := &FoundryProvisioningProvider{azdClient: p.azdClient, envName: p.envName}
	require.NoError(t, p2.loadACRLifecycle(t.Context()))
	assert.Equal(t, p.acrPending, p2.acrPending)
	env.configErr = errors.New("disk full")
	p.acrPending = nil
	require.ErrorContains(t, p.prepareACRAccess(t.Context(), acrTestSource()), "disk full")
	assert.Nil(t, p.acrPending)
}

func TestACRCleanupExactOwnership(t *testing.T) {
	for _, scenario := range []string{"owned", "already gone", "administrator", "403", "wrong principal", "wrong role"} {
		t.Run(scenario, func(t *testing.T) {
			p, _ := acrTestProvider(t)
			p.acrPending = acrTestPending()
			p.acrPending.PrincipalID = "principal"
			deletes := []string{}
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				if method == http.MethodDelete {
					deletes = append(deletes, path)
					return nil
				}
				if strings.Contains(path, "/roleAssignments/") {
					switch scenario {
					case "already gone":
						return &azcore.ResponseError{StatusCode: http.StatusNotFound}
					case "403":
						return &azcore.ResponseError{StatusCode: http.StatusForbidden}
					}
					actual := acrTestAssignment()
					switch scenario {
					case "administrator":
						actual.Properties.Description = ""
					case "wrong principal":
						actual.Properties.PrincipalID = "other"
					case "wrong role":
						actual.Properties.RoleDefinitionID = "/roles/owner"
					}
					return acrTestReply(out, actual)
				}
				return &azcore.ResponseError{StatusCode: http.StatusNotFound}
			}
			err := p.cleanupACRAccess(t.Context())
			if scenario == "owned" || scenario == "already gone" {
				require.NoError(t, err)
				if scenario == "owned" {
					assert.Equal(t, []string{p.acrPending.assignmentID()}, deletes)
				} else {
					assert.Empty(t, deletes)
				}
			} else {
				require.Error(t, err)
				assert.Empty(t, deletes)
			}
			assert.NotNil(t, p.acrPending)
		})
	}
}

func TestACRPartialRecoveryAndBinding(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrPending = acrTestPending()
	data, err := json.Marshal(p.acrPending)
	require.NoError(t, err)
	env.config = map[string][]byte{acrPendingConfigKey: data}
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodGet, method)
		if path == testACRProject {
			return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
		}
		require.Equal(t, p.acrPending.assignmentID(), path)
		return acrTestReply(out, acrTestAssignment())
	}
	require.NoError(t, p.recoverACRAssignment(t.Context()))
	assert.Equal(t, "principal", p.acrPending.PrincipalID)
	p.greenfieldAcrMode = "reuse-connect"
	require.NoError(t, p.persistACRBinding(t.Context(), map[string]*azdext.ProvisioningOutputParameter{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": {Value: testACRID},
		"AZURE_AI_PROJECT_ID":                  {Value: testACRProject},
	}))
	require.NotNil(t, p.acrBinding)
	require.Len(t, p.acrBinding.OwnedAssignments, 1)
	assert.Nil(t, p.acrPending)
	assert.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey])
	assert.NotContains(t, env.config, acrPendingConfigKey)
	assert.Equal(t, "reuse-connect", env.set["AZD_FOUNDRY_ACR_MODE"])
}

func TestACRBindingRejectsEjectedMigration(t *testing.T) {
	p, _ := acrTestProvider(t)
	p.acrBinding = acrTestBinding()
	src := acrTestSource()
	require.NoError(t, p.validateACRTemplate(src))
	src.parameters["existingAcrResourceId"] = map[string]any{"value": testACRID + "other"}
	require.ErrorContains(t, p.validateACRTemplate(src), "migration")
	src.parameters["existingAcrResourceId"] = map[string]any{"value": ""}
	require.ErrorContains(t, p.validateACRTemplate(src), "migration")
}

func TestACRTemplateSubscriptionValidationBeforeCloudCalls(t *testing.T) {
	for _, mode := range []string{"embedded", "ejected"} {
		for _, subscription := range []string{"other", ""} {
			t.Run(mode+"/"+subscription, func(t *testing.T) {
				p, env := acrTestProvider(t)
				p.projectPath = t.TempDir()
				p.subID = subscription
				p.acrRequest = func(context.Context, string, string, string, any) error {
					t.Fatal("invalid registry scope must be rejected before ARM requests")
					return nil
				}
				if mode == "ejected" {
					p.onDiskSource = acrTestSource()
				} else {
					p.armTemplate = acrTestSource().armTemplate
					p.synthResult = &synthesis.Result{Parameters: map[string]any{
						"includeAcr": true, "existingAcrResourceId": testACRID,
					}}
				}
				_, err := p.Deploy(t.Context(), func(string) {})
				if subscription == "" {
					require.ErrorContains(t, err, "AZURE_SUBSCRIPTION_ID is required")
				} else {
					require.ErrorContains(t, err, "same subscription")
				}
				assert.Empty(t, env.config)
				assert.Empty(t, env.set)
				assert.Nil(t, p.acrPending)
			})
		}
	}
}

func TestACRLegacyBindingUsesDeploymentFacts(t *testing.T) {
	p, env := acrTestProvider(t)
	p.greenfieldAcrMode = "reuse-connect"
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodGet, method)
		if path == testACRProject {
			return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
		}
		require.Contains(t, path, "/providers/Microsoft.Resources/deployments/")
		return acrTestReply(out, map[string]any{"properties": map[string]any{
			"outputs": map[string]any{
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": testACRID},
				"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
			},
			"parameters": map[string]any{"existingAcrResourceId": map[string]any{"value": testACRID}},
		}})
	}
	require.NoError(t, p.recoverDeployedACRBinding(t.Context()))
	require.NotNil(t, p.acrBinding)
	assert.Empty(t, p.acrBinding.OwnedAssignments)
	assert.Empty(t, env.config)
	assert.Empty(t, env.set)
	src := acrTestSource()
	src.parameters["existingAcrResourceId"] = map[string]any{"value": testACRID + "other"}
	require.ErrorContains(t, p.prepareACRAccess(t.Context(), src), "migration")
	assert.Empty(t, env.config)
	assert.Empty(t, env.set)
}

func TestACRCreatedOutputRequiresDeploymentFacts(t *testing.T) {
	const registryID = "/subscriptions/sub/resourceGroups/foundry/" +
		"providers/Microsoft.ContainerRegistry/registries/registry"
	for _, tt := range []struct {
		name, deployedID, wantError string
	}{
		{name: "missing deployment", wantError: "cannot establish ownership"},
		{name: "matching deployment", deployedID: registryID},
		{name: "different deployed registry", deployedID: registryID + "other", wantError: "migration"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.synthesisEnv = map[string]string{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registryID}
			src := acrTestSource()
			src.parameters["existingAcrResourceId"] = map[string]any{"value": ""}
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				if tt.deployedID == "" {
					return &azcore.ResponseError{StatusCode: http.StatusNotFound}
				}
				if path == testACRProject {
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				}
				require.Contains(t, path, "/providers/Microsoft.Resources/deployments/")
				return acrTestReply(out, map[string]any{"properties": map[string]any{
					"outputs": map[string]any{
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": tt.deployedID},
						"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
					},
				}})
			}
			require.NoError(t, p.recoverDeployedACRBinding(t.Context()))
			err := p.prepareACRAccess(t.Context(), src)
			if tt.wantError != "" {
				require.ErrorContains(t, err, tt.wantError)
				assert.Empty(t, env.config)
			} else {
				require.NoError(t, err)
				require.NotNil(t, p.acrBinding)
				assert.Equal(t, "create", p.acrBinding.Mode)
				assert.Equal(t, registryID, p.acrBinding.RegistryResourceID)
				assert.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey])
			}
			assert.Empty(t, env.set)
		})
	}
}

func TestACRUnverifiedLegacyOutputCannotDeleteResourceGroup(t *testing.T) {
	p, env := acrTestProvider(t)
	p.rgExplicit = true
	p.synthesisEnv = map[string]string{
		"AZURE_SUBSCRIPTION_ID":                "sub",
		"AZURE_AI_PROJECT_ID":                  testACRProject,
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": strings.Replace(testACRID, "/shared/", "/foundry/", 1),
		"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "registry.azurecr.io",
	}
	p.acrRequest = func(_ context.Context, method, path, _ string, _ any) error {
		require.Equal(t, http.MethodGet, method)
		require.Contains(t, path, "/providers/Microsoft.Resources/deployments/")
		return &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.ErrorContains(t, err, "cannot establish ownership")
	assert.Empty(t, env.set)
	assert.Empty(t, env.config)
	assert.Nil(t, p.credential, "unverified ownership must fail before destructive Azure operations")
}

func TestACRPartialConnectionRecoveryRequiresCreationEvidence(t *testing.T) {
	const resourcesID = "/subscriptions/sub/resourceGroups/foundry/" +
		"providers/Microsoft.Resources/deployments/foundry-resources"
	const acrID = "/subscriptions/sub/resourceGroups/foundry/providers/Microsoft.Resources/deployments/acr"
	const ownedRegistry = "/subscriptions/sub/resourceGroups/foundry/" +
		"providers/Microsoft.ContainerRegistry/registries/registry"
	operation := func(id, resourceType, kind, state string) map[string]any {
		return map[string]any{"properties": map[string]any{
			"targetResource":        map[string]any{"id": id, "resourceType": resourceType},
			"provisioningOperation": kind,
			"provisioningState":     state,
		}}
	}
	for _, tt := range []struct {
		name, wantMode, wantError string
		pending                   bool
	}{
		{name: "owned legacy", wantMode: "create"},
		{name: "owned partial", pending: true, wantMode: "create"},
		{name: "canceled deployment", pending: true, wantMode: "create"},
		{name: "paginated evidence", wantMode: "create"},
		{name: "external other group", pending: true, wantMode: "reuse-connect"},
		{name: "external same group", pending: true, wantError: "no successful creation operation"},
		{name: "missing evidence", pending: true, wantError: "cannot establish ownership"},
		{name: "missing history", wantError: "cannot establish ownership"},
		{name: "missing evidence page", wantError: "verify partially deployed registry ownership"},
		{name: "repeated evidence page", wantError: "pagination repeated a page"},
		{name: "missing operation kind", wantError: "no successful creation operation"},
		{name: "failed creation", wantError: "no successful creation operation"},
		{name: "different registry", wantError: "cannot establish ownership"},
		{name: "ambiguous evidence", wantError: "multiple registry creation operations"},
		{name: "ambiguous connections", wantError: "multiple deployed registry connections"},
		{name: "unavailable evidence", pending: true, wantError: "verify partially deployed registry ownership"},
		{name: "canceled recovery", pending: true, wantError: "context canceled"},
	} {
		for _, action := range []string{"recover", "down partial", "down legacy"} {
			if action != "recover" && tt.wantError == "" {
				continue
			}
			t.Run(tt.name+"/"+action, func(t *testing.T) {
				p, env := acrTestProvider(t)
				rootID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
				p.rgExplicit = true
				p.credential = acrOfflineCredential{}
				if tt.pending {
					p.acrPending = acrTestPending()
					require.NoError(t, p.writeACRConfig(t.Context(), acrPendingConfigKey, p.acrPending))
				}
				pending := p.acrPending
				before := maps.Clone(env.config)
				p.synthesisEnv = map[string]string{}
				if action == "down legacy" {
					p.synthesisEnv = map[string]string{
						"AZURE_SUBSCRIPTION_ID":                "sub",
						"AZURE_AI_PROJECT_ID":                  testACRProject,
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": ownedRegistry,
						"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "registry.azurecr.io",
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					require.Equal(t, http.MethodGet, method, "recovery must not mutate Azure resources or access")
					switch path {
					case rootID:
						state := "Failed"
						if tt.name == "canceled deployment" {
							state = "Canceled"
						}
						return acrTestReply(out, map[string]any{"properties": map[string]any{
							"provisioningState": state,
							"parameters":        map[string]any{"resourceTokenSalt": map[string]any{"value": "salt"}},
						}})
					case resourcesID + "/operations":
						return acrTestReply(out, map[string]any{"value": []any{
							operation(testACRProject, "Microsoft.CognitiveServices/accounts/projects", "Create", "Succeeded"),
							operation(acrID, "Microsoft.Resources/deployments", "Create", "Failed"),
						}})
					case testACRProject:
						return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
					case testACRProject + "/connections":
						registryID := ownedRegistry
						if tt.name == "external other group" {
							registryID = testACRID
						}
						connections := []any{map[string]any{"properties": map[string]any{
							"category": "ContainerRegistry", "metadata": map[string]string{"ResourceId": registryID},
						}}}
						if tt.name == "ambiguous connections" {
							connections = append(connections, map[string]any{"properties": map[string]any{
								"category": "ContainerRegistry", "metadata": map[string]string{"ResourceId": registryID + "other"},
							}})
						}
						return acrTestReply(out, map[string]any{"value": connections})
					case rootID + "/operations":
						switch tt.name {
						case "missing history":
							return &azcore.ResponseError{StatusCode: http.StatusNotFound}
						case "unavailable evidence":
							return &azcore.ResponseError{StatusCode: http.StatusForbidden}
						case "canceled recovery":
							cancel()
						}
						return acrTestReply(out, map[string]any{"value": []any{
							operation(resourcesID, "Microsoft.Resources/deployments", "Create", "Failed"),
						}})
					case acrID + "/operations", acrID + "/operations?after=first":
						registryID, kind, state := ownedRegistry, "Create", "Succeeded"
						switch tt.name {
						case "external same group":
							kind = "Read"
						case "missing evidence":
							return acrTestReply(out, map[string]any{"value": []any{}})
						case "missing operation kind":
							kind = ""
						case "failed creation":
							state = "Failed"
						case "different registry":
							registryID += "other"
						case "paginated evidence", "missing evidence page", "repeated evidence page":
							if !strings.Contains(path, "?") {
								next := acrID + "/operations?after=first"
								if tt.name == "repeated evidence page" {
									next = path
								}
								return acrTestReply(out, map[string]any{
									"value": []any{}, "nextLink": next,
								})
							}
							if tt.name == "missing evidence page" {
								return &azcore.ResponseError{StatusCode: http.StatusNotFound}
							}
						}
						created := operation(registryID, "Microsoft.ContainerRegistry/registries", kind, state)
						operations := []any{created}
						if tt.name == "ambiguous evidence" {
							operations = append(operations, created)
						}
						return acrTestReply(out, map[string]any{"value": operations})
					default:
						t.Fatalf("unexpected recovery request: %s %s", method, path)
						return nil
					}
				}
				var err error
				if action == "recover" {
					err = p.recoverDeployedACRBinding(ctx)
				} else {
					_, err = p.Destroy(ctx, &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
				}
				if tt.wantError != "" {
					require.ErrorContains(t, err, tt.wantError)
					if tt.name == "canceled recovery" {
						require.ErrorIs(t, err, context.Canceled)
					}
					assert.Nil(t, p.acrBinding)
					assert.False(t, p.acrBindingRecovered)
				} else {
					require.NoError(t, err)
					require.NotNil(t, p.acrBinding)
					assert.Equal(t, tt.wantMode, p.acrBinding.Mode)
					assert.Equal(t, testACRProject, p.acrBinding.ProjectResourceID)
					assert.Equal(t, "principal", p.acrBinding.PrincipalID)
					assert.Equal(t, "salt", p.acrBinding.DeploymentSalt)
					assert.Empty(t, p.acrBinding.OwnedAssignments)
					assert.True(t, p.acrBindingRecovered)
				}
				assert.Same(t, pending, p.acrPending)
				assert.Equal(t, before, env.config)
				assert.Empty(t, env.set)
			})
		}
	}
}

func TestACRPendingIntentDoesNotFreezeUndeployedRegistry(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrPending = acrTestPending()
	newID := testACRID + "other"
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodGet, method)
		if path == newID {
			return acrTestReply(out, map[string]any{"properties": map[string]any{}})
		}
		return &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	src := acrTestSource()
	src.parameters["existingAcrResourceId"] = map[string]any{"value": newID}
	require.NoError(t, p.prepareACRAccess(t.Context(), src))
	require.NotNil(t, p.acrPending)
	assert.Equal(t, newID, p.acrPending.RegistryResourceID)
	assert.NotEqual(t, testACRToken, p.acrPending.Token)
	assert.NotEmpty(t, env.config[acrPendingConfigKey])
}

func TestACRSuccessfulRepeatDoesNotRewriteLifecycleState(t *testing.T) {
	p, env := acrTestProvider(t)
	p.greenfieldAcrMode = "reuse-connect"
	p.acrBinding = acrTestBinding()
	env.configErr = errors.New("state must not be written")
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodGet, method)
		require.Equal(t, testACRProject, path)
		return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
	}
	require.NoError(t, p.persistACRBinding(t.Context(), map[string]*azdext.ProvisioningOutputParameter{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": {Value: testACRID},
		"AZURE_AI_PROJECT_ID":                  {Value: testACRProject},
	}))
	assert.Empty(t, env.config)
	assert.Empty(t, env.set)
}

func TestACRCleanupDeploymentRecords(t *testing.T) {
	p, _ := acrTestProvider(t)
	p.acrPending = acrTestPending()
	deletes := []string{}
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		if method == http.MethodDelete {
			deletes = append(deletes, path)
			return nil
		}
		if strings.Contains(path, "/roleAssignments/") {
			return acrTestReply(out, acrTestAssignment())
		}
		params := map[string]any{
			"accessOperationId": map[string]any{"value": testACRToken},
		}
		if strings.Contains(path, "azd-acr-access-") {
			params = map[string]any{
				"assignmentName":       map[string]any{"value": testACRToken},
				"ownershipDescription": map[string]any{"value": "azd-foundry:" + testACRToken},
			}
		}
		return acrTestReply(out, map[string]any{"properties": map[string]any{"parameters": params}})
	}
	require.NoError(t, p.cleanupACRAccess(t.Context()))
	assert.Equal(t, append([]string{p.acrPending.assignmentID()}, p.acrPending.DeploymentIDs...), deletes)
	assert.NotContains(t, deletes, testACRID)
}

func TestACRDestroyNoPromptPreservesOwnedAccess(t *testing.T) {
	t.Setenv("AZD_NO_PROMPT", "true")
	p, _ := acrTestProvider(t)
	p.acrPending = acrTestPending()
	p.rgExplicit = true
	p.acrRequest = func(context.Context, string, string, string, any) error {
		t.Fatal("must not access registry before destroy confirmation")
		return nil
	}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{}, func(string) {})
	require.Error(t, err)
	assert.NotNil(t, p.acrPending)
}

type acrOfflineCredential struct{}

func (acrOfflineCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, errors.New("cloud calls are forbidden in this test")
}

func TestACRDestroyMissingGroupRecoversPartialAccess(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrPending = acrTestPending()
	p.credential = acrOfflineCredential{}
	p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
		return nil, false, nil
	}
	deleted := []string{}
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		if method == http.MethodDelete {
			deleted = append(deleted, path)
			return nil
		}
		if strings.Contains(path, "/roleAssignments/") {
			return acrTestReply(out, acrTestAssignment())
		}
		return &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []string{
		acrTestPending().assignmentID(),
		"/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName(),
	}, deleted)
	assert.Nil(t, p.acrPending)
	assert.NotContains(t, env.config, acrPendingConfigKey)
}

type acrTestTransport func(*http.Request) (*http.Response, error)

func (f acrTestTransport) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type acrTestCredential struct{}

func (acrTestCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func acrTestHTTPReply(t *testing.T, request *http.Request, code int, value any) (*http.Response, error) {
	t.Helper()
	body, err := json.Marshal(value)
	require.NoError(t, err)
	if code == http.StatusNoContent {
		body = nil
	}
	return &http.Response{
		StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(string(body))), Request: request,
	}, nil
}

func TestACRProjectOnlyProvisionPreservesPendingSelector(t *testing.T) {
	for _, tt := range []struct {
		mode string
		down bool
	}{
		{mode: ""}, {mode: "reuse-connect"},
		{mode: "", down: true}, {mode: "reuse-connect", down: true},
	} {
		t.Run(tt.mode+"/down="+strconv.FormatBool(tt.down), func(t *testing.T) {
			root := t.TempDir()
			manifest := "services:\n  project:\n    host: azure.ai.project\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(manifest), 0o600))
			p, env := acrTestProvider(t)
			env.get = map[string]string{
				"AZURE_SUBSCRIPTION_ID":                "sub",
				"AZURE_LOCATION":                       "eastus2",
				"AZURE_RESOURCE_GROUP":                 "foundry",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
				"AZD_FOUNDRY_ACR_MODE":                 tt.mode,
			}
			require.NoError(t, p.Initialize(t.Context(), root,
				&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
			require.Equal(t, "none", p.greenfieldAcrMode)
			p.credential = acrTestCredential{}
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodGet &&
						strings.HasSuffix(request.URL.Path, "/Microsoft.CognitiveServices/accounts") {
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"value": []any{}})
					}
					if request.Method == http.MethodDelete {
						if strings.EqualFold(request.URL.Path, "/subscriptions/sub/resourceGroups/foundry") {
							return acrTestHTTPReply(t, request, http.StatusOK, nil)
						}
						require.Equal(t, "/subscriptions/sub/providers/Microsoft.Resources/deployments/"+
							p.deploymentName(), request.URL.Path)
						return acrTestHTTPReply(t, request, http.StatusNoContent, nil)
					}
					require.Contains(t, request.URL.Path, "/providers/Microsoft.Resources/deployments/")
					if request.Method == http.MethodGet {
						return acrTestHTTPReply(t, request, http.StatusNotFound, nil)
					}
					require.Equal(t, http.MethodPut, request.Method)
					outputs := map[string]any{
						"AZURE_AI_PROJECT_ID": map[string]any{"type": "string", "value": testACRProject},
					}
					for _, key := range []string{
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
						"AZURE_CONTAINER_REGISTRY_ENDPOINT",
						"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
					} {
						outputs[key] = map[string]any{"type": "string", "value": ""}
					}
					return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
						"properties": map[string]any{"provisioningState": "Succeeded", "outputs": outputs},
					})
				}),
			}}
			result, err := p.Deploy(t.Context(), func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result.Deployment)
			values := maps.Clone(env.get)
			maps.Copy(values, env.set)
			for key, output := range result.Deployment.Outputs {
				values[key] = output.Value
			}
			envPath := filepath.Join(root, ".azure", "test", ".env")
			require.NoError(t, os.MkdirAll(filepath.Dir(envPath), 0o750))
			require.NoError(t, godotenv.Write(values, envPath))
			env.get, err = godotenv.Read(envPath)
			require.NoError(t, err)
			assert.Equal(t, testACRID, env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
			assert.Equal(t, tt.mode, env.get["AZD_FOUNDRY_ACR_MODE"])
			assert.Empty(t, env.config)
			assert.Nil(t, p.acrBinding)

			if tt.down {
				destroyed, err := p.Destroy(t.Context(),
					&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
				require.NoError(t, err)
				require.NotNil(t, destroyed)
				assert.NotContains(t, destroyed.InvalidatedEnvKeys, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
				assert.Contains(t, destroyed.InvalidatedEnvKeys, "AZD_FOUNDRY_ACR_MODE")
				for _, key := range destroyed.InvalidatedEnvKeys {
					delete(env.get, key)
				}
				require.NoError(t, godotenv.Write(env.get, envPath))
				env.get, err = godotenv.Read(envPath)
				require.NoError(t, err)
				require.Equal(t, testACRID, env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				assert.Empty(t, env.config)
				assert.Nil(t, p.acrBinding)
			}

			manifest += "  agent:\n    host: azure.ai.agent\n    kind: hosted\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(manifest), 0o600))
			next := &FoundryProvisioningProvider{azdClient: p.azdClient}
			require.NoError(t, next.Initialize(t.Context(), root,
				&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
			assert.Equal(t, "reuse-connect", next.greenfieldAcrMode)
			assert.Equal(t, map[string]any{"value": testACRID}, next.armParameters()["existingAcrResourceId"])
		})
	}
}

func TestACRFailedDeploymentRecoversIndependentOwnership(t *testing.T) {
	for _, tt := range []struct {
		name               string
		cancelled          bool
		beginFailure       bool
		assignmentFailure  bool
		persistenceFailure bool
		ownershipFailure   bool
		preexistingGroup   bool
	}{
		{name: "cancelled deployment", cancelled: true},
		{name: "uncertain submission", beginFailure: true},
		{name: "assignment read fails", assignmentFailure: true},
		{name: "assignment save fails", persistenceFailure: true},
		{name: "both recoveries fail", assignmentFailure: true, ownershipFailure: true},
		{name: "preexisting group is not adopted", assignmentFailure: true, preexistingGroup: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
				"services:\n  project:\n    host: azure.ai.project\n"+
					"  agent:\n    host: azure.ai.agent\n    kind: hosted\n"), 0o600))
			p, env := acrTestProvider(t)
			env.get = map[string]string{
				envKeySubscriptionID: "sub", envKeyLocation: "eastus2", envKeyProjectName: "project",
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
			}
			require.NoError(t, p.Initialize(t.Context(), root,
				&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
			require.Equal(t, "rg-test", p.rgName)
			require.False(t, p.rgExplicit)
			p.credential = acrTestCredential{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			submitted, assignmentRead, assignmentDeleted, groupDeleted := false, false, false, false
			groupID := "/subscriptions/sub/resourceGroups/rg-test"
			p.resourceGroupState = func(ctx context.Context) (map[string]*string, bool, error) {
				if submitted {
					assert.NoError(t, ctx.Err())
					deadline, ok := ctx.Deadline()
					assert.True(t, ok)
					assert.LessOrEqual(t, time.Until(deadline), 30*time.Second)
					if tt.ownershipFailure {
						return nil, false, errors.New("resource-group recovery unavailable")
					}
				}
				return map[string]*string{"azd-env-name": new("test")}, submitted || tt.preexistingGroup, nil
			}
			transport := acrTestTransport(func(request *http.Request) (*http.Response, error) {
				switch {
				case request.Method == http.MethodPut:
					submitted = true
					if tt.persistenceFailure {
						env.configErr = errors.New("assignment recovery cannot save state")
					}
					if tt.cancelled {
						cancel()
					}
					if tt.beginFailure {
						return nil, errors.New("connection lost after submission")
					}
					return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
						"properties": map[string]any{
							"provisioningState": "Failed",
							"error":             map[string]any{"code": "DeploymentFailed", "message": "deployment failed"},
						},
					})
				case request.Method == http.MethodGet && strings.EqualFold(request.URL.Path, groupID):
					return acrTestHTTPReply(t, request, http.StatusOK,
						map[string]any{"tags": map[string]string{"azd-env-name": "test"}})
				case request.Method == http.MethodGet &&
					strings.HasSuffix(request.URL.Path, "/Microsoft.CognitiveServices/accounts"):
					return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"value": []any{}})
				case request.Method == http.MethodDelete && strings.EqualFold(request.URL.Path, groupID):
					groupDeleted = true
					return acrTestHTTPReply(t, request, http.StatusOK, nil)
				default:
					t.Fatalf("unexpected ARM request: %s %s", request.Method, request.URL.Path)
					return nil, errors.New("unexpected ARM request")
				}
			})
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: transport, Retry: policy.RetryOptions{MaxRetries: -1},
			}}
			recovering := true
			p.acrRequest = func(ctx context.Context, method, path, _ string, out any) error {
				switch {
				case path == testACRID:
					return acrTestReply(out, map[string]any{"properties": map[string]any{}})
				case strings.Contains(path, "/roleAssignments/"):
					if method == http.MethodDelete {
						assignmentDeleted = true
						return nil
					}
					assignmentRead = true
					if recovering {
						assert.NoError(t, ctx.Err())
						_, bounded := ctx.Deadline()
						assert.True(t, bounded)
						if tt.assignmentFailure {
							return errors.New("assignment recovery unavailable")
						}
					}
					actual := acrTestAssignment()
					require.NotNil(t, p.acrPending)
					actual.ID = p.acrPending.assignmentID()
					actual.Properties.Description = "azd-foundry:" + p.acrPending.Token
					return acrTestReply(out, actual)
				default:
					require.Contains(t, path, "/Microsoft.Resources/deployments/")
					return &azcore.ResponseError{StatusCode: http.StatusNotFound}
				}
			}
			result, err := p.Deploy(ctx, func(string) {})
			require.Error(t, err)
			require.Nil(t, result)
			require.True(t, submitted)
			assert.True(t, assignmentRead)
			assert.NotEmpty(t, env.config[acrPendingConfigKey])
			if tt.assignmentFailure {
				assert.ErrorContains(t, err, "assignment recovery unavailable")
			}
			if tt.persistenceFailure {
				assert.ErrorContains(t, err, "assignment recovery cannot save state")
			}
			if tt.ownershipFailure {
				assert.ErrorContains(t, err, "resource-group recovery unavailable")
				return
			}
			if tt.preexistingGroup {
				assert.Empty(t, env.set[envKeyFoundryRGOwner])
				assert.Empty(t, p.foundryRGOwnerID)
				return
			}
			require.Equal(t, groupID, env.set[envKeyFoundryRGOwner])
			require.Equal(t, groupID, p.foundryRGOwnerID)
			maps.Copy(env.get, env.set)
			env.configErr = nil
			recovering = false
			next := &FoundryProvisioningProvider{azdClient: p.azdClient}
			require.NoError(t, next.Initialize(t.Context(), root,
				&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
			next.credential, next.armClientOptions, next.acrRequest = p.credential, p.armClientOptions, p.acrRequest
			destroyed, err := next.Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, destroyed)
			assert.True(t, assignmentDeleted)
			assert.True(t, groupDeleted)
			assert.Empty(t, env.config)
		})
	}
}

func TestACRPreviewUsesReadOnlyAccessPlan(t *testing.T) {
	for _, tt := range []struct {
		name                                         string
		history                                      string
		abac, granted, pending, newProject, embedded bool
	}{
		{name: "classic existing access", granted: true},
		{name: "ABAC existing access", abac: true, granted: true},
		{name: "classic missing access"},
		{name: "ABAC missing access", abac: true},
		{name: "pending owned access", granted: true, pending: true},
		{name: "project-only deployment", history: "project-only", granted: true},
		{name: "legacy registry binding", history: "registry", granted: true},
		{name: "new project", newProject: true},
		{name: "embedded existing access", granted: true, embedded: true},
		{name: "embedded missing access", embedded: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.projectPath = t.TempDir()
			p.location = "eastus2"
			p.credential = acrTestCredential{}
			p.onDiskSource = acrTestSource()
			if tt.embedded {
				p.armTemplate = p.onDiskSource.armTemplate
				p.onDiskSource = nil
				p.synthResult = &synthesis.Result{Parameters: map[string]any{
					"includeAcr": true, "existingAcrResourceId": testACRID, "resourceTokenSalt": "",
				}}
			}
			p.acrBinding = acrTestBinding()
			if tt.pending {
				p.acrPending = acrTestPending()
			}
			if tt.pending || tt.newProject || tt.history != "" {
				p.acrBinding = nil
			}
			pendingBefore, err := json.Marshal(p.acrPending)
			require.NoError(t, err)
			env.configErr = errors.New("preview must not persist registry lifecycle state")
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				switch path {
				case testACRID:
					mode := "LegacyRegistryPermissions"
					if tt.abac {
						mode = "AbacRepositoryPermissions"
					}
					return acrTestReply(out, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
				case testACRProject:
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				case acrTestPending().assignmentID():
					return acrTestReply(out, acrTestAssignment())
				default:
					if strings.Contains(path, "/providers/Microsoft.Resources/deployments/") {
						if tt.history == "" {
							return &azcore.ResponseError{StatusCode: http.StatusNotFound}
						}
						outputs := map[string]any{
							"AZURE_AI_PROJECT_ID": map[string]any{"value": testACRProject},
						}
						if tt.history == "registry" {
							outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = map[string]any{"value": testACRID}
						}
						return acrTestReply(out, map[string]any{"properties": map[string]any{
							"provisioningState": "Succeeded", "outputs": outputs,
							"parameters": map[string]any{
								"existingAcrResourceId": map[string]any{"value": testACRID},
							},
						}})
					}
					require.Contains(t, path, "roleAssignments?")
					assignments := []acrRoleAssignment{}
					if tt.granted {
						grant := acrTestAssignment()
						if tt.abac {
							grant.Properties.RoleDefinitionID = "/roles/" + acrRepositoryReader
						}
						assignments = append(assignments, grant)
					}
					return acrTestReply(out, map[string]any{"value": assignments})
				}
			}
			var parameters map[string]struct{ Value any }
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					require.Equal(t, http.MethodPost, request.Method)
					require.True(t, strings.HasSuffix(request.URL.Path, "/whatIf"))
					var submitted struct {
						Properties struct {
							Parameters map[string]struct{ Value any }
						}
					}
					require.NoError(t, json.NewDecoder(request.Body).Decode(&submitted))
					parameters = submitted.Properties.Parameters
					return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
						"status": "Succeeded", "properties": map[string]any{"changes": []any{}},
					})
				}),
			}}
			_, err = p.Preview(t.Context(), func(string) {})
			require.NoError(t, err)
			require.Contains(t, parameters, "acrPullAssigned")
			assert.Equal(t, tt.granted, parameters["acrPullAssigned"].Value)
			if tt.pending {
				assert.Equal(t, testACRToken, parameters["acrAccessOperationId"].Value)
			}
			pendingAfter, err := json.Marshal(p.acrPending)
			require.NoError(t, err)
			assert.Equal(t, pendingBefore, pendingAfter)
			assert.Empty(t, env.config)
			assert.Empty(t, env.set)

			env.configErr = nil
			src, err := p.resolveTemplate(t.Context(), func(string) {})
			require.NoError(t, err)
			require.NoError(t, p.prepareACRAccess(t.Context(), src))
			assert.Equal(t, parameters["acrPullAssigned"].Value, parameterValue(src, "acrPullAssigned"))
		})
	}
}

func TestACRPreviewAccessFailureDoesNotSubmitWhatIf(t *testing.T) {
	for _, failure := range []string{"registry", "project", "assignments", "history", "pending", "pending identity"} {
		t.Run(failure, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.projectPath = t.TempDir()
			p.onDiskSource = acrTestSource()
			p.acrBinding = acrTestBinding()
			if failure == "history" {
				p.acrBinding = nil
			}
			if failure == "pending" || failure == "pending identity" {
				p.acrPending = acrTestPending()
			}
			pendingBefore, err := json.Marshal(p.acrPending)
			require.NoError(t, err)
			readErr := errors.New("registry access read unavailable")
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				switch path {
				case testACRID:
					if failure == "registry" {
						return readErr
					}
					return acrTestReply(out, map[string]any{"properties": map[string]any{}})
				case testACRProject:
					if failure == "project" {
						return readErr
					}
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				case acrTestPending().assignmentID():
					if failure == "pending identity" {
						assignment := acrTestAssignment()
						assignment.Properties.PrincipalID = "other-principal"
						return acrTestReply(out, assignment)
					}
					require.Equal(t, "pending", failure)
					return readErr
				default:
					if failure == "history" {
						require.Contains(t, path, "/providers/Microsoft.Resources/deployments/")
					} else {
						require.Equal(t, "assignments", failure)
						require.Contains(t, path, "roleAssignments?")
					}
					return readErr
				}
			}
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: acrTestTransport(func(*http.Request) (*http.Response, error) {
					t.Fatal("preview must not submit what-if when access planning fails")
					return nil, readErr
				}),
			}}
			p.credential = acrTestCredential{}
			_, err = p.Preview(t.Context(), func(string) {})
			if failure == "pending identity" {
				require.ErrorContains(t, err, "current Foundry project identity")
				err = p.prepareACRAccess(t.Context(), acrTestSource())
				require.ErrorContains(t, err, "current Foundry project identity")
			} else {
				require.ErrorIs(t, err, readErr)
			}
			pendingAfter, err := json.Marshal(p.acrPending)
			require.NoError(t, err)
			assert.Equal(t, pendingBefore, pendingAfter)
			assert.Empty(t, env.config)
			assert.Empty(t, env.set)
		})
	}
}

func TestACRDestroyThenDeploy(t *testing.T) {
	for _, mode := range []string{"create", "reuse-connect", "none"} {
		for _, missingGroup := range []bool{false, true} {
			name := mode + "/existing group"
			if missingGroup {
				name = mode + "/missing group"
			}
			t.Run(name, func(t *testing.T) {
				p, env := acrTestProvider(t)
				p.projectPath = t.TempDir()
				p.rgExplicit = true
				p.location = "eastus2"
				p.credential = acrTestCredential{}
				p.greenfieldAcrMode = mode
				p.armTemplate = acrTestSource().armTemplate
				p.synthResult = &synthesis.Result{Parameters: map[string]any{"includeAcr": mode != "none"}}
				registryID := strings.Replace(testACRID, "/shared/", "/foundry/", 1)
				if mode == "reuse-connect" {
					registryID = testACRID
					p.synthResult.Parameters["existingAcrResourceId"] = registryID
				}
				if mode != "none" {
					p.acrBinding = acrTestBinding()
					p.acrBinding.Mode = mode
					p.acrBinding.RegistryResourceID = registryID
					data, err := json.Marshal(p.acrBinding)
					require.NoError(t, err)
					env.config = map[string][]byte{synthesis.ACRBindingConfigKey: data}
				}
				groupID := "/subscriptions/sub/resourceGroups/foundry"
				deploymentID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
				groupGone, deploymentExists, created := missingGroup, true, false
				retired := false
				operationID := ""
				transport := acrTestTransport(func(request *http.Request) (*http.Response, error) {
					reply := func(code int, value any) (*http.Response, error) {
						return acrTestHTTPReply(t, request, code, value)
					}
					path := request.URL.Path
					switch {
					case request.Method == http.MethodGet && strings.HasSuffix(path, "/Microsoft.CognitiveServices/accounts"):
						return reply(http.StatusOK, map[string]any{"value": []any{}})
					case request.Method == http.MethodDelete && strings.EqualFold(path, groupID):
						groupGone = true
						if missingGroup {
							return reply(http.StatusNotFound, nil)
						}
						return reply(http.StatusOK, nil)
					case request.Method == http.MethodGet && strings.EqualFold(path, groupID):
						require.True(t, groupGone)
						return reply(http.StatusNotFound, nil)
					case request.Method == http.MethodDelete && path == deploymentID:
						require.True(t, groupGone)
						if mode == "create" && !missingGroup {
							response, err := reply(http.StatusAccepted, nil)
							response.Header.Set("Location", "https://management.azure.com/operations/retire")
							return response, err
						}
						deploymentExists = false
						retired = true
						return reply(http.StatusNoContent, nil)
					case request.Method == http.MethodGet && path == "/operations/retire":
						require.NotEmpty(t, env.config)
						deploymentExists = false
						retired = true
						return reply(http.StatusNoContent, nil)
					case request.Method == http.MethodGet && path == deploymentID:
						if !deploymentExists {
							return reply(http.StatusNotFound, nil)
						}
						outputs := map[string]any{"AZURE_AI_PROJECT_ID": map[string]any{"value": testACRProject}}
						if mode != "none" {
							outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = map[string]any{"value": registryID}
						}
						return reply(http.StatusOK, map[string]any{"properties": map[string]any{
							"provisioningState": "Succeeded", "outputs": outputs,
						}})
					case request.Method == http.MethodPut && path == deploymentID:
						require.True(t, retired)
						require.False(t, deploymentExists)
						var deployment struct {
							Properties struct {
								Parameters map[string]struct{ Value any }
							}
						}
						require.NoError(t, json.NewDecoder(request.Body).Decode(&deployment))
						operationID, _ = deployment.Properties.Parameters["acrAccessOperationId"].Value.(string)
						deploymentExists, created = true, true
						outputs := map[string]any{
							"AZURE_AI_PROJECT_ID":           map[string]any{"value": testACRProject},
							"AZD_FOUNDRY_RESOURCE_GROUP_ID": map[string]any{"value": groupID},
						}
						if mode != "none" {
							outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = map[string]any{"value": registryID}
						}
						return reply(http.StatusOK, map[string]any{"properties": map[string]any{
							"provisioningState": "Succeeded", "outputs": outputs,
						}})
					case request.Method == http.MethodGet && path == testACRProject:
						require.True(t, created, "must not recover the deleted project's identity")
						return reply(http.StatusOK, map[string]any{"identity": map[string]any{"principalId": "principal"}})
					case request.Method == http.MethodGet && path == testACRID:
						return reply(http.StatusOK, map[string]any{"properties": map[string]any{}})
					case request.Method == http.MethodGet && strings.Contains(path, "/roleAssignments/"):
						require.NotEmpty(t, operationID)
						assignment := acrTestAssignment()
						assignment.ID = path
						assignment.Properties.Description = "azd-foundry:" + operationID
						return reply(http.StatusOK, assignment)
					default:
						t.Fatalf("unexpected ARM request: %s %s", request.Method, path)
						return nil, errors.New("unexpected ARM request")
					}
				})
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: transport}}
				result, err := p.Destroy(t.Context(),
					&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
				require.NoError(t, err)
				require.NotNil(t, result)
				require.True(t, retired)
				require.Empty(t, env.config)
				next := &FoundryProvisioningProvider{
					azdClient: p.azdClient, projectPath: p.projectPath, envName: p.envName,
					subID: p.subID, rgName: p.rgName, location: p.location, foundryName: p.foundryName,
					credential: p.credential, armClientOptions: p.armClientOptions,
					armTemplate: p.armTemplate, synthResult: p.synthResult, greenfieldAcrMode: mode,
				}
				deployed, err := next.Deploy(t.Context(), func(string) {})
				require.NoError(t, err)
				require.NotNil(t, deployed)
				require.True(t, created)
				if mode != "none" {
					require.NotNil(t, next.acrBinding)
					assert.Equal(t, mode, next.acrBinding.Mode)
					assert.Equal(t, registryID, next.acrBinding.RegistryResourceID)
				}
			})
		}
	}
}

func TestACRDestroyRetirementFailurePreservesRecovery(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrBinding = acrTestBinding()
	p.credential = acrOfflineCredential{}
	p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
		return nil, false, nil
	}
	data, err := json.Marshal(p.acrBinding)
	require.NoError(t, err)
	env.config = map[string][]byte{synthesis.ACRBindingConfigKey: data}
	deploymentID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
	fail := true
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodDelete, method)
		require.Equal(t, deploymentID, path)
		require.Nil(t, out)
		if fail {
			return &azcore.ResponseError{StatusCode: http.StatusForbidden}
		}
		return &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.ErrorContains(t, err, "delete completed Foundry deployment record")
	require.Nil(t, result)
	assert.NotNil(t, p.acrBinding)
	assert.JSONEq(t, string(data), string(env.config[synthesis.ACRBindingConfigKey]))
	fail = false
	env.configErr = errors.New("cannot clear local recovery state")
	result, err = p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.ErrorContains(t, err, "cannot clear local recovery state")
	require.Nil(t, result)
	assert.NotNil(t, p.acrBinding)
	assert.JSONEq(t, string(data), string(env.config[synthesis.ACRBindingConfigKey]))
	env.configErr = nil
	result, err = p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, p.acrBinding)
	assert.Empty(t, env.config)
}

func TestACRDestroyFailureDoesNotRetireDeployment(t *testing.T) {
	for _, failAt := range []string{"resource group", "purge"} {
		t.Run(failAt, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.acrBinding = acrTestBinding()
			p.rgExplicit = true
			p.credential = acrTestCredential{}
			data, err := json.Marshal(p.acrBinding)
			require.NoError(t, err)
			env.config = map[string][]byte{synthesis.ACRBindingConfigKey: data}
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					statusCode := http.StatusOK
					var body string
					switch {
					case request.Method == http.MethodGet &&
						strings.HasSuffix(request.URL.Path, "/Microsoft.CognitiveServices/accounts"):
						body = `{"value":[{"name":"account","location":"eastus2"}]}`
					case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/deployments"):
						body = `{"value":[]}`
					case request.Method == http.MethodDelete &&
						strings.HasSuffix(strings.ToLower(request.URL.Path), "/resourcegroups/foundry"):
						if failAt == "resource group" {
							statusCode = http.StatusForbidden
						}
					case request.Method == http.MethodDelete && strings.HasSuffix(request.URL.Path, "/deletedAccounts/account"):
						require.Equal(t, "purge", failAt)
						statusCode = http.StatusForbidden
					default:
						t.Fatalf("unexpected ARM request: %s %s", request.Method, request.URL.Path)
					}
					if statusCode == http.StatusForbidden {
						body = `{"error":{"code":"AuthorizationFailed","message":"teardown failed"}}`
					}
					return &http.Response{
						StatusCode: statusCode, Header: http.Header{"Content-Type": {"application/json"}},
						Body: io.NopCloser(strings.NewReader(body)), Request: request,
					}, nil
				}),
			}}
			result, err := p.Destroy(t.Context(),
				&azdext.ProvisioningDestroyOptions{Force: true, Purge: true}, func(string) {})
			require.ErrorContains(t, err, "teardown failed")
			require.Nil(t, result)
			assert.NotNil(t, p.acrBinding)
			assert.JSONEq(t, string(data), string(env.config[synthesis.ACRBindingConfigKey]))
		})
	}
}

func TestACRGrantRecoveryFailureRetainsDurableIntent(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrPending = acrTestPending()
	p.greenfieldAcrMode = "reuse-connect"
	data, err := json.Marshal(p.acrPending)
	require.NoError(t, err)
	env.config = map[string][]byte{acrPendingConfigKey: data}
	env.configErr = errors.New("cannot save state")
	p.acrRequest = func(_ context.Context, _, path, _ string, out any) error {
		if path == testACRProject {
			return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
		}
		return acrTestReply(out, acrTestAssignment())
	}
	err = p.persistACRBinding(t.Context(), map[string]*azdext.ProvisioningOutputParameter{
		"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": {Value: testACRID},
		"AZURE_AI_PROJECT_ID":                  {Value: testACRProject},
	})
	require.ErrorContains(t, err, "cannot save state")
	assert.JSONEq(t, string(data), string(env.config[acrPendingConfigKey]))
	assert.NotContains(t, env.config, synthesis.ACRBindingConfigKey)
	assert.NotNil(t, p.acrPending)
}

func TestACRCustomRolePullAccess(t *testing.T) {
	for _, tt := range []struct {
		name                           string
		abac                           bool
		actions, dataActions, excluded []string
		want                           bool
	}{
		{name: "classic wildcard", actions: []string{"Microsoft.ContainerRegistry/*"}, want: true},
		{name: "classic exclusions", actions: []string{"*"}, excluded: []string{"*/pull/read"}},
		{name: "ABAC data actions", abac: true,
			dataActions: []string{"Microsoft.ContainerRegistry/registries/repositories/*/read"}, want: true},
		{name: "ABAC ignores management actions", abac: true, actions: []string{"*"}},
		{name: "ABAC requires content and metadata", abac: true,
			dataActions: []string{"Microsoft.ContainerRegistry/registries/repositories/content/read"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := acrTestProvider(t)
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				require.Equal(t, "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/custom", path)
				permission := map[string]any{
					"actions": tt.actions, "dataActions": tt.dataActions,
					"notActions": tt.excluded, "notDataActions": tt.excluded,
				}
				return acrTestReply(out, map[string]any{"properties": map[string]any{
					"permissions": []any{permission},
				}})
			}
			allowed, err := p.customACRPull(t.Context(),
				"/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/custom", tt.abac)
			require.NoError(t, err)
			assert.Equal(t, tt.want, allowed)
		})
	}
}

func TestACRRoleAssignmentConflictIsNarrow(t *testing.T) {
	assert.True(t, acrRoleAssignmentConflict(&azcore.ResponseError{ErrorCode: "RoleAssignmentExists", StatusCode: 409}))
	assert.False(t, acrRoleAssignmentConflict(&azcore.ResponseError{ErrorCode: "RoleAssignmentExists", StatusCode: 403}))
	assert.False(t, acrRoleAssignmentConflict(errors.New("RoleAssignmentExists")))
	for _, tt := range []struct {
		body string
		want bool
	}{
		{`{"error":{"code":"DeploymentFailed","details":[{"code":"RoleAssignmentExists"}]}}`, true},
		{`{"properties":{"error":{"code":"ResourceDeploymentFailure","details":[{"code":"RoleAssignmentExists"}]}}}`, true},
		{`{"error":{"code":"DeploymentFailed","details":[{"code":"RoleAssignmentExists"},{"code":"AuthorizationFailed"}]}}`, false},
		{`{"error":{"code":"DeploymentFailed","message":"RoleAssignmentExists"}}`, false},
	} {
		response := &azcore.ResponseError{ErrorCode: "DeploymentFailed", RawResponse: &http.Response{
			Body: io.NopCloser(strings.NewReader(tt.body)), StatusCode: http.StatusConflict,
		}}
		assert.Equal(t, tt.want, acrRoleAssignmentConflict(response), tt.body)
	}
}

func TestACRProjectOnlyDeploymentDoesNotDuplicateAdministratorAccess(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodGet, method)
		switch {
		case path == testACRProject:
			return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
		case path == testACRID:
			return acrTestReply(out, map[string]any{"properties": map[string]any{}})
		case strings.Contains(path, "/roleAssignments?"):
			grant := acrTestAssignment()
			grant.ID = testACRID + "/providers/Microsoft.Authorization/roleAssignments/administrator"
			grant.Properties.Description = ""
			return acrTestReply(out, map[string]any{"value": []acrRoleAssignment{grant}})
		default:
			return acrTestReply(out, map[string]any{"properties": map[string]any{
				"provisioningState": "Succeeded",
				"outputs": map[string]any{
					"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": ""},
				},
			}})
		}
	}
	require.NoError(t, p.recoverDeployedACRBinding(t.Context()))
	assert.Nil(t, p.acrBinding)
	assert.Equal(t, "principal", p.acrExistingPrincipalID)
	src := acrTestSource()
	require.NoError(t, p.prepareACRAccess(t.Context(), src))
	assert.Equal(t, true, parameterValue(src, "acrPullAssigned"))
	assert.Nil(t, p.acrPending)
	assert.Empty(t, env.config)
}

func TestACROrphanCleanupChecksRecordedDeploymentGroup(t *testing.T) {
	p, _ := acrTestProvider(t)
	p.acrPending = acrTestPending()
	p.credential = acrOfflineCredential{}
	p.rgName = "unrecorded-default"
	p.resourceGroupState = func(context.Context) (map[string]*string, bool, error) {
		assert.Equal(t, "foundry", p.rgName)
		return nil, true, nil
	}
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		if method == http.MethodDelete {
			require.Equal(t, acrTestPending().assignmentID(), path)
			return nil
		}
		if strings.Contains(path, "/roleAssignments/") {
			return acrTestReply(out, acrTestAssignment())
		}
		return &azcore.ResponseError{StatusCode: http.StatusNotFound}
	}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
	require.ErrorContains(t, err, "cannot be proven owned")
	assert.NotNil(t, p.acrPending)
	assert.Equal(t, "unrecorded-default", p.rgName)
}
