// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
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
			"acrPullRoleDefinitionId": map[string]any{}, "acrOwnedAssignments": map[string]any{},
			"existingAcrResourceId": map[string]any{},
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

func TestACRManagedProjectOnlyRepeatRetainsCreationEvidence(t *testing.T) {
	for _, missingEvidence := range []bool{false, true} {
		t.Run(strconv.FormatBool(missingEvidence), func(t *testing.T) {
			p, env := acrTestProvider(t)
			registryID := strings.Replace(testACRID, "/shared/", "/foundry/", 1)
			rootID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
			creatorID := "/subscriptions/sub/resourceGroups/foundry/providers/Microsoft.Resources/deployments/acr"
			p.acrBinding = acrTestBinding()
			p.acrBinding.Mode = "create"
			p.acrBinding.RegistryResourceID = registryID
			src := acrTestSource()
			setParameterValue(src, "includeAcr", false)
			setParameterValue(src, "existingAcrResourceId", "")
			tags := map[string]*string{}
			projectOnly := false
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, http.MethodGet, method)
				switch path {
				case rootID:
					id := registryID
					if projectOnly {
						id = ""
					}
					return acrTestReply(out, map[string]any{
						"tags": tags, "properties": map[string]any{
							"provisioningState": "Succeeded",
							"outputs": map[string]any{
								"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
								"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": id},
							},
							"parameters": map[string]any{"existingAcrResourceId": map[string]any{"value": ""}},
						},
					})
				case rootID + "/operations":
					require.False(t, projectOnly, "repeat must use retained nested evidence")
					return acrTestReply(out, map[string]any{"value": []any{map[string]any{
						"properties": map[string]any{
							"targetResource": map[string]any{
								"id": creatorID, "resourceType": "Microsoft.Resources/deployments",
							},
							"provisioningOperation": "Create", "provisioningState": "Succeeded",
						},
					}}})
				case creatorID + "/operations":
					if missingEvidence && projectOnly {
						return &azcore.ResponseError{StatusCode: http.StatusNotFound}
					}
					return acrTestReply(out, map[string]any{"value": []any{map[string]any{
						"properties": map[string]any{
							"targetResource": map[string]any{
								"id": registryID, "resourceType": "Microsoft.ContainerRegistry/registries",
							},
							"provisioningOperation": "Create", "provisioningState": "Succeeded",
						},
					}}})
				case testACRProject:
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				default:
					t.Fatalf("unexpected recovery request: %s", path)
					return errors.New("unexpected recovery request")
				}
			}
			require.NoError(t, p.retainManagedACREvidence(t.Context(), src, tags))
			require.Equal(t, "registry", *tags[acrManagedRegistryTag])
			require.Equal(t, "acr", *tags[acrManagedDeploymentTag])
			projectOnly = true
			if !missingEvidence {
				require.NoError(t, p.retainManagedACREvidence(t.Context(), src, tags))
			}
			for _, recoverIdentity := range []bool{false, true} {
				p.acrBinding = nil
				p.acrBindingRecovered = false
				err := p.recoverACRBinding(t.Context(), recoverIdentity)
				if missingEvidence {
					require.ErrorContains(t, err, "no successful registry creation operation")
					require.Nil(t, p.acrBinding)
					continue
				}
				require.NoError(t, err)
				require.NoError(t, p.validateACRCreateBinding(registryID))
				require.Equal(t, registryID, p.acrBinding.RegistryResourceID)
				require.Equal(t, "create", p.acrBinding.Mode)
				require.NoError(t, p.persistRecoveredACRBinding(t.Context()))
				require.Contains(t, env.config, synthesis.ACRBindingConfigKey)
			}
		})
	}
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

func TestACRTemplateParameterCasing(t *testing.T) {
	for _, defaults := range []bool{false, true} {
		for _, unsafe := range []bool{false, true} {
			t.Run(fmt.Sprintf("defaults=%t/unsafe=%t", defaults, unsafe), func(t *testing.T) {
				p, env := acrTestProvider(t)
				id := testACRID
				if unsafe {
					id = strings.Replace(id, "/shared/", "/foundry/", 1)
				}
				source := acrTestSource()
				supplied := map[string]any{
					"IncludeACR": true, "ExistingAcrResourceId": id, "ResourceGroupName": "foundry",
				}
				if defaults {
					source.parameters = nil
					params := map[string]any{}
					for name, value := range supplied {
						params[name] = map[string]any{"defaultValue": value}
					}
					source.armTemplate["parameters"] = params
				} else {
					parameters := map[string]any{}
					for name, value := range supplied {
						parameters[name] = map[string]any{"value": value}
					}
					source.parameters = mergeParameters(parameters, source.parameters)
				}
				err := p.validateACRTemplate(source)
				if unsafe {
					require.ErrorContains(t, err, "azd down deletes")
					assert.Empty(t, env.config)
					assert.Empty(t, env.set)
					return
				}
				require.NoError(t, err)
				require.Equal(t, "reuse-connect", p.greenfieldAcrMode)
				p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					require.Equal(t, http.MethodGet, method)
					require.Equal(t, testACRProject, path)
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				}
				require.NoError(t, p.persistACRBinding(t.Context(), map[string]*azdext.ProvisioningOutputParameter{
					"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": {Value: id},
					"AZURE_AI_PROJECT_ID":                  {Value: testACRProject},
				}))
				require.NotNil(t, p.acrBinding)
				assert.Equal(t, "reuse-connect", p.acrBinding.Mode)
			})
		}
	}
}

func TestACRParameterWritesReplaceCaseAliases(t *testing.T) {
	p, _ := acrTestProvider(t)
	p.acrBinding = acrTestBinding()
	assigned := false
	p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
		require.Equal(t, http.MethodGet, method)
		switch {
		case path == testACRID:
			return acrTestReply(out, map[string]any{"properties": map[string]any{}})
		case path == testACRProject:
			return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
		case strings.Contains(path, "/roleAssignments?"):
			grants := []acrRoleAssignment{}
			if assigned {
				grant := acrTestAssignment()
				grant.Properties.Description = ""
				grants = append(grants, grant)
			}
			return acrTestReply(out, map[string]any{"value": grants})
		case strings.Contains(path, "/roleAssignments/"):
			return &azcore.ResponseError{StatusCode: http.StatusNotFound}
		default:
			t.Fatalf("unexpected registry request: %s", path)
			return errors.New("unexpected registry request")
		}
	}
	src := acrTestSource()
	submittedParameters := src.parameters
	for _, present := range []bool{false, true} {
		assigned = present
		maps.Copy(src.parameters, map[string]any{
			"AcrPullAssigned":         map[string]any{"value": !present},
			"ACRPullRoleDefinitionId": map[string]any{"value": "stale-role"},
			"AcrAccessOperationId":    map[string]any{"value": "stale-operation"},
			"ACROwnedAssignments":     map[string]any{"value": []string{"stale-assignment"}},
		})
		require.NoError(t, p.prepareACRAccess(t.Context(), src))
		require.NotNil(t, p.acrPending)
		for name, want := range map[string]any{
			"acrPullAssigned":         present,
			"acrPullRoleDefinitionId": p.acrPending.RoleDefinitionID,
			"acrAccessOperationId":    p.acrPending.Token,
			"acrOwnedAssignments":     []synthesis.ACRAssignment{},
		} {
			names := []string{}
			for key := range submittedParameters {
				if strings.EqualFold(key, name) {
					names = append(names, key)
				}
			}
			require.Equal(t, []string{name}, names)
			assert.Equal(t, map[string]any{"value": want}, submittedParameters[name])
			assert.Equal(t, want, parameterValue(src, name))
		}
	}
}

func TestACRRetainedParameterWritesReplaceCaseAliases(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		empty, casing, owned bool
	}{
		{name: "aliases"},
		{name: "empty parameters", empty: true},
		{name: "alternate declarations", casing: true},
		{name: "retained grants alternate declarations", casing: true, owned: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, _ := acrTestProvider(t)
			p.acrBinding = acrTestBinding()
			owned := []synthesis.ACRAssignment{}
			if tt.owned {
				owned = append(owned, synthesis.ACRAssignment{
					ID: acrTestAssignment().ID, PrincipalID: "principal", OwnershipToken: testACRToken,
					RoleDefinitionID: acrTestPending().RoleDefinitionID,
					DeploymentIDs:    acrAccessDeploymentIDs(testACRID, testACRToken),
				})
				p.acrBinding.OwnedAssignments = owned
			}
			src := acrTestSource()
			if tt.casing {
				declared, ok := src.armTemplate["parameters"].(map[string]any)
				require.True(t, ok)
				renamed := map[string]any{}
				for name, value := range declared {
					renamed[strings.ToUpper(name)] = value
				}
				src.armTemplate["parameters"] = renamed
			}
			src.parameters = nil
			if !tt.empty {
				src.parameters = map[string]any{
					"ExistingACRResourceId": map[string]any{"value": ""},
					"ACROwnedAssignments":   map[string]any{"value": []string{"stale-assignment"}},
				}
			}
			plan, err := p.planACRAccess(t.Context(), src)
			require.NoError(t, err)
			assert.Nil(t, plan)
			assert.Equal(t, map[string]any{
				"existingAcrResourceId": map[string]any{"value": testACRID},
				"acrOwnedAssignments":   map[string]any{"value": owned},
			}, src.parameters)
		})
	}
}

func TestACRTemplateNetworkingUsesEffectiveValues(t *testing.T) {
	for _, defaults := range []bool{false, true} {
		for _, tt := range []struct {
			name, wantError  string
			network, hosting any
			external, noACR  bool
		}{
			{name: "private auto-created registry", network: true,
				wantError: "auto-created Azure Container Registry"},
			{name: "public auto-created registry", network: false},
			{name: "private external registry", network: true, external: true},
			{name: "private without registry", network: true, noACR: true},
			{name: "private AKS hosting", network: true,
				hosting: map[string]any{"enabled": true, "clusterResourceId": offlineClusterID}},
			{name: "disabled AKS hosting", network: true, hosting: map[string]any{"enabled": false},
				wantError: "auto-created Azure Container Registry"},
			{name: "invalid network value", network: "true", wantError: "enableNetworkIsolation must be a literal boolean"},
			{name: "null network value", wantError: "enableNetworkIsolation must be a literal boolean"},
			{name: "invalid hosting value", network: true, hosting: "invalid",
				wantError: "invalid agentHosting deployment parameters"},
		} {
			t.Run(fmt.Sprintf("%s/defaults=%t", tt.name, defaults), func(t *testing.T) {
				p, env := acrTestProvider(t)
				src := acrTestSource()
				src.parameters["includeAcr"] = map[string]any{"value": !tt.noACR}
				id := ""
				if tt.external {
					id = testACRID
				}
				src.parameters["existingAcrResourceId"] = map[string]any{"value": id}
				declared, ok := src.armTemplate["parameters"].(map[string]any)
				require.True(t, ok)
				declared["EnableNetworkIsolation"] = map[string]any{"type": "bool", "defaultValue": tt.network}
				declared["AgentHosting"] = map[string]any{"type": "object", "defaultValue": tt.hosting}
				if !defaults {
					declared["EnableNetworkIsolation"] = map[string]any{"type": "bool", "defaultValue": false}
					declared["AgentHosting"] = map[string]any{
						"type": "object", "defaultValue": map[string]any{"enabled": true},
					}
					src.parameters["ENABLENETWORKISOLATION"] = map[string]any{"value": tt.network}
					src.parameters["AGENTHOSTING"] = map[string]any{"value": tt.hosting}
				}
				err := p.validateACRTemplate(src)
				if tt.wantError != "" {
					require.ErrorContains(t, err, tt.wantError)
				} else {
					require.NoError(t, err)
				}
				assert.Empty(t, env.set)
				assert.Empty(t, env.config)
			})
		}
	}
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

func TestACRDeploymentGrantRecoveryAfterStateLoss(t *testing.T) {
	for _, tt := range []struct {
		name, role                                                string
		partial, missing, unowned                                 bool
		projectOnly, noConnection, connection404, pendingSelector bool
	}{
		{name: "classic", role: acrPullRole},
		{name: "ABAC", role: acrRepositoryReader},
		{name: "partial", role: acrPullRole, partial: true},
		{name: "grant already deleted", role: acrPullRole, missing: true},
		{name: "partial grant already deleted", role: acrRepositoryReader, partial: true, missing: true},
		{name: "administrator access", role: acrPullRole, unowned: true},
		{name: "project-only administrator access", role: acrPullRole, unowned: true, projectOnly: true},
		{name: "unused project-only selector", role: acrPullRole, unowned: true,
			projectOnly: true, noConnection: true, pendingSelector: true},
		{name: "failed connection", role: acrPullRole, partial: true, noConnection: true},
		{name: "missing connection API", role: acrRepositoryReader, partial: true, connection404: true},
		{name: "failed connection grant already deleted", role: acrPullRole,
			partial: true, noConnection: true, missing: true},
	} {
		for _, action := range []string{"recover", "down"} {
			t.Run(tt.name+"/"+action, func(t *testing.T) {
				p, env := acrTestProvider(t)
				p.rgExplicit = true
				p.credential = acrTestCredential{}
				require.NoError(t, p.loadACRLifecycle(t.Context()))
				require.Nil(t, p.acrBinding)
				require.Nil(t, p.acrPending)
				assignment := acrTestAssignment()
				assignment.Properties.RoleDefinitionID = "/subscriptions/sub/providers/Microsoft.Authorization/roleDefinitions/" + tt.role
				owned := synthesis.ACRAssignment{
					ID: assignment.ID, PrincipalID: "principal",
					RoleDefinitionID: assignment.Properties.RoleDefinitionID, OwnershipToken: testACRToken,
					DeploymentIDs: acrAccessDeploymentIDs(testACRID, testACRToken),
				}
				rootID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
				resourcesID := "/subscriptions/sub/resourceGroups/foundry/" +
					"providers/Microsoft.Resources/deployments/foundry-resources"
				deleted := []string{}
				groupDeleted := false
				p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					if method == http.MethodDelete {
						require.Equal(t, "down", action)
						if !tt.unowned {
							require.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey])
						}
						if path == rootID {
							require.True(t, groupDeleted)
						}
						deleted = append(deleted, path)
						return nil
					}
					require.Equal(t, http.MethodGet, method)
					switch path {
					case rootID:
						state := "Succeeded"
						outputs := map[string]any{
							"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": testACRID},
							"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
						}
						if tt.partial {
							state, outputs = "Failed", nil
						} else if tt.projectOnly {
							outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = map[string]any{"value": ""}
						}
						parameters := map[string]any{
							"includeAcr":              map[string]any{"value": !tt.projectOnly},
							"acrOwnedAssignments":     map[string]any{"value": []synthesis.ACRAssignment{}},
							"existingAcrResourceId":   map[string]any{"value": testACRID},
							"acrAccessOperationId":    map[string]any{"value": testACRToken},
							"acrPullRoleDefinitionId": map[string]any{"value": owned.RoleDefinitionID},
						}
						if tt.unowned {
							parameters["acrAccessOperationId"] = map[string]any{"value": ""}
						}
						if tt.name == "ABAC" {
							for _, name := range []string{"acrAccessOperationId", "acrPullRoleDefinitionId"} {
								parameters[strings.ToUpper(name)] = parameters[name]
								delete(parameters, name)
							}
						}
						return acrTestReply(out, map[string]any{"properties": map[string]any{
							"provisioningState": state, "outputs": outputs,
							"parameters": parameters,
						}})
					case testACRProject:
						return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
					case resourcesID + "/operations":
						return acrTestReply(out, map[string]any{"value": []any{map[string]any{"properties": map[string]any{
							"targetResource": map[string]any{
								"id": testACRProject, "resourceType": "Microsoft.CognitiveServices/accounts/projects",
							},
						}}}})
					case testACRProject + "/connections":
						if tt.connection404 {
							return &azcore.ResponseError{StatusCode: http.StatusNotFound}
						}
						if tt.noConnection {
							return acrTestReply(out, map[string]any{"value": []any{}})
						}
						return acrTestReply(out, map[string]any{"value": []any{map[string]any{"properties": map[string]any{
							"category": "ContainerRegistry", "metadata": map[string]string{"ResourceId": testACRID},
						}}}})
					case owned.ID:
						require.False(t, tt.unowned, "administrator grants must remain outside ownership recovery")
						if tt.missing {
							return &azcore.ResponseError{StatusCode: http.StatusNotFound}
						}
						return acrTestReply(out, assignment)
					case owned.DeploymentIDs[0], owned.DeploymentIDs[1]:
						parameters := map[string]any{
							"accessOperationId": map[string]any{"value": testACRToken},
						}
						if path == owned.DeploymentIDs[0] {
							parameters = map[string]any{
								"assignmentName":       map[string]any{"value": testACRToken},
								"ownershipDescription": map[string]any{"value": "azd-foundry:" + testACRToken},
							}
						}
						return acrTestReply(out, map[string]any{"properties": map[string]any{
							"parameters": parameters,
						}})
					default:
						t.Fatalf("unexpected recovery request: %s %s", method, path)
						return nil
					}
				}
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
					Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
						require.Equal(t, "down", action)
						if request.Method == http.MethodDelete {
							require.True(t, strings.EqualFold("/subscriptions/sub/resourceGroups/foundry", request.URL.Path))
							if !tt.unowned {
								require.Contains(t, deleted, owned.DeploymentIDs[0])
								require.Contains(t, deleted, owned.DeploymentIDs[1])
							}
							groupDeleted = true
							return acrTestHTTPReply(t, request, http.StatusOK, nil)
						}
						require.Equal(t, http.MethodGet, request.Method)
						require.True(t, strings.HasSuffix(request.URL.Path, "/Microsoft.CognitiveServices/accounts"))
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"value": []any{}})
					}),
				}}
				if action == "recover" {
					require.NoError(t, p.recoverDeployedACRBinding(t.Context()))
					if tt.pendingSelector {
						require.Nil(t, p.acrBinding)
						src := acrTestSource()
						src.parameters["existingAcrResourceId"] = map[string]any{"value": testACRID + "other"}
						require.NoError(t, p.validateACRTemplate(src))
						return
					}
					require.NotNil(t, p.acrBinding)
					if tt.unowned {
						assert.Empty(t, p.acrBinding.OwnedAssignments)
						if tt.projectOnly {
							src := acrTestSource()
							src.parameters["existingAcrResourceId"] = map[string]any{"value": testACRID + "other"}
							require.ErrorContains(t, p.validateACRTemplate(src), "migration is not supported")
						}
					} else {
						assert.Equal(t, []synthesis.ACRAssignment{owned}, p.acrBinding.OwnedAssignments)
					}
					assert.Empty(t, deleted)
				} else {
					result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
					require.NoError(t, err)
					require.NotNil(t, result)
					expected := slices.Clone(owned.DeploymentIDs)
					if tt.unowned {
						expected = nil
					} else if !tt.missing {
						expected = append([]string{owned.ID}, expected...)
					}
					assert.Equal(t, append(expected, rootID), deleted)
					assert.True(t, groupDeleted)
					assert.Nil(t, p.acrBinding)
				}
				assert.Nil(t, p.acrPending)
				assert.Empty(t, env.config)
				assert.Empty(t, env.set)
			})
		}
	}
}

func TestACRDeploymentGrantRecoveryRejectsUnverifiedOwnership(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, tt := range []struct {
			name, wantError string
		}{
			{name: "administrator grant", wantError: "does not match azd ownership"},
			{name: "wrong principal", wantError: "does not match azd ownership"},
			{name: "wrong role", wantError: "does not match azd ownership"},
			{name: "wrong scope", wantError: "does not match azd ownership"},
			{name: "wrong assignment", wantError: "does not match azd ownership"},
			{name: "conditional grant", wantError: "does not match azd ownership"},
			{name: "unreadable grant", wantError: "verify recovered registry grant ownership"},
			{name: "invalid token", wantError: "cannot recover registry grant ownership"},
			{name: "invalid token type", wantError: "cannot recover registry grant ownership"},
			{name: "null token", wantError: "cannot recover registry grant ownership"},
			{name: "missing role", wantError: "cannot recover registry grant ownership"},
			{name: "invalid role", wantError: "cannot recover registry grant ownership"},
			{name: "invalid role type", wantError: "cannot recover registry grant ownership"},
			{name: "retained null", wantError: "invalid retained assignments"},
			{name: "retained invalid type", wantError: "cannot recover retained registry grant ownership"},
			{name: "retained unknown fields", wantError: "cannot recover retained registry grant ownership"},
			{name: "retained wrong principal", wantError: "cannot recover registry grant ownership"},
			{name: "retained wrong role", wantError: "cannot recover registry grant ownership"},
			{name: "retained wrong deployment", wantError: "cannot recover registry grant ownership"},
			{name: "retained administrator grant", wantError: "does not match azd ownership"},
			{name: "active deployment", wantError: "deployment is still active"},
		} {
			t.Run(fmt.Sprintf("%s/partial=%t", tt.name, partial), func(t *testing.T) {
				p, env := acrTestProvider(t)
				p.rgExplicit = true
				p.credential = acrOfflineCredential{}
				assignment := acrTestAssignment()
				parameters := map[string]any{
					"existingAcrResourceId":   map[string]any{"value": testACRID},
					"acrAccessOperationId":    map[string]any{"value": testACRToken},
					"acrPullRoleDefinitionId": map[string]any{"value": assignment.Properties.RoleDefinitionID},
				}
				state := "Succeeded"
				if strings.HasPrefix(tt.name, "retained ") {
					parameters["acrAccessOperationId"] = map[string]any{"value": ""}
					owned := synthesis.ACRAssignment{
						ID: assignment.ID, PrincipalID: "principal",
						RoleDefinitionID: assignment.Properties.RoleDefinitionID, OwnershipToken: testACRToken,
						DeploymentIDs: acrAccessDeploymentIDs(testACRID, testACRToken),
					}
					switch tt.name {
					case "retained wrong principal":
						owned.PrincipalID = "another-principal"
					case "retained wrong role":
						owned.RoleDefinitionID = "/roles/owner"
					case "retained wrong deployment":
						owned.DeploymentIDs[0] += "-other"
					case "retained administrator grant":
						assignment.Properties.Description = ""
					}
					parameters["acrOwnedAssignments"] = map[string]any{"value": []synthesis.ACRAssignment{owned}}
				}
				switch tt.name {
				case "administrator grant":
					assignment.Properties.Description = ""
				case "wrong principal":
					assignment.Properties.PrincipalID = "other"
				case "wrong role":
					assignment.Properties.RoleDefinitionID = "/roles/owner"
				case "wrong scope":
					assignment.Properties.Scope = "/subscriptions/sub"
				case "wrong assignment":
					assignment.ID += "other"
				case "conditional grant":
					assignment.Properties.Condition = "condition"
				case "invalid token":
					parameters["acrAccessOperationId"] = map[string]any{"value": "invalid"}
				case "invalid token type":
					parameters["acrAccessOperationId"] = map[string]any{"value": 123}
				case "null token":
					parameters["acrAccessOperationId"] = map[string]any{"value": nil}
				case "missing role":
					delete(parameters, "acrPullRoleDefinitionId")
				case "invalid role":
					parameters["acrPullRoleDefinitionId"] = map[string]any{"value": "/roles/owner"}
				case "invalid role type":
					parameters["acrPullRoleDefinitionId"] = map[string]any{"value": true}
				case "retained null":
					parameters["acrOwnedAssignments"] = map[string]any{"value": nil}
				case "retained invalid type":
					parameters["acrOwnedAssignments"] = map[string]any{"value": "not-an-array"}
				case "retained unknown fields":
					parameters["acrOwnedAssignments"] = map[string]any{"value": []map[string]any{{"unexpected": true}}}
				case "active deployment":
					state = "Running"
				}
				registryID := testACRID
				if partial {
					registryID = ""
					if state == "Succeeded" {
						state = "Failed"
					}
				}
				rootID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
				p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					require.Equal(t, http.MethodGet, method, "unverified ownership must not mutate resources")
					switch path {
					case rootID:
						return acrTestReply(out, map[string]any{"properties": map[string]any{
							"provisioningState": state, "parameters": parameters,
							"outputs": map[string]any{
								"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": registryID},
								"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
							},
						}})
					case testACRProject:
						return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
					case testACRProject + "/connections":
						return acrTestReply(out, map[string]any{"value": []any{}})
					case acrTestAssignment().ID:
						if tt.name == "unreadable grant" {
							return &azcore.ResponseError{StatusCode: http.StatusForbidden}
						}
						return acrTestReply(out, assignment)
					default:
						t.Fatalf("unexpected ownership request: %s %s", method, path)
						return nil
					}
				}
				result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
				require.ErrorContains(t, err, tt.wantError)
				assert.Nil(t, result)
				assert.Nil(t, p.acrBinding)
				assert.False(t, p.acrBindingRecovered)
				assert.Nil(t, p.acrPending)
				assert.Empty(t, env.config)
				assert.Empty(t, env.set)
			})
		}
	}
}

func TestACRRecoveredGrantOwnershipIsPersistedBeforeCleanup(t *testing.T) {
	for _, failure := range []string{"save", "delete"} {
		t.Run(failure, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.acrBinding = acrTestBinding()
			assignment := acrTestAssignment()
			p.acrBinding.OwnedAssignments = []synthesis.ACRAssignment{{
				ID: assignment.ID, PrincipalID: "principal",
				RoleDefinitionID: assignment.Properties.RoleDefinitionID, OwnershipToken: testACRToken,
				DeploymentIDs: acrAccessDeploymentIDs(testACRID, testACRToken),
			}}
			p.acrBindingRecovered = true
			if failure == "save" {
				env.configErr = errors.New("cannot save recovered ownership")
			}
			deletes := 0
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.Equal(t, "delete", failure, "failed persistence must prevent ARM mutations")
				require.Equal(t, assignment.ID, path)
				if method == http.MethodDelete {
					deletes++
					require.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey])
					return &azcore.ResponseError{StatusCode: http.StatusForbidden}
				}
				require.Equal(t, http.MethodGet, method)
				return acrTestReply(out, assignment)
			}
			err := p.cleanupACRAccess(t.Context())
			if failure == "save" {
				require.ErrorContains(t, err, "cannot save recovered ownership")
				assert.Zero(t, deletes)
				assert.True(t, p.acrBindingRecovered)
				assert.Empty(t, env.config)
			} else {
				require.ErrorContains(t, err, "delete azd-owned registry grant")
				assert.Equal(t, 1, deletes)
				assert.False(t, p.acrBindingRecovered)
				reloaded := &FoundryProvisioningProvider{azdClient: p.azdClient, envName: p.envName}
				require.NoError(t, reloaded.loadACRLifecycle(t.Context()))
				assert.Equal(t, p.acrBinding, reloaded.acrBinding)
			}
		})
	}
}

func TestACRRecoveredBindingPersistedWithoutOwnedAssignments(t *testing.T) {
	p, env := acrTestProvider(t)
	p.acrBinding = acrTestBinding()
	p.acrBinding.OwnedAssignments = nil
	p.acrBindingRecovered = true
	p.acrRequest = func(context.Context, string, string, string, any) error {
		t.Fatal("admin-provided pull must not mutate ARM grants")
		return nil
	}
	require.NoError(t, p.cleanupACRAccess(t.Context()))
	require.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey])
	assert.False(t, p.acrBindingRecovered)
	reloaded := &FoundryProvisioningProvider{azdClient: p.azdClient, envName: p.envName}
	require.NoError(t, reloaded.loadACRLifecycle(t.Context()))
	assert.Equal(t, p.acrBinding, reloaded.acrBinding)
	assert.Empty(t, reloaded.acrBinding.OwnedAssignments)
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
				if strings.HasSuffix(path, "/operations") {
					return acrTestReply(out, map[string]any{"value": []any{map[string]any{
						"properties": map[string]any{
							"targetResource": map[string]any{
								"id": tt.deployedID, "resourceType": "Microsoft.ContainerRegistry/registries",
							},
							"provisioningOperation": "Create", "provisioningState": "Succeeded",
						},
					}}})
				}
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

func TestACRFullOutputRecoveryRequiresCreationEvidence(t *testing.T) {
	const registryID = "/subscriptions/sub/resourceGroups/foundry/" +
		"providers/Microsoft.ContainerRegistry/registries/registry"
	const nestedID = "/subscriptions/sub/resourceGroups/foundry/" +
		"providers/Microsoft.Resources/deployments/acr"
	const referenceID = "/subscriptions/sub/resourceGroups/foundry/" +
		"providers/Microsoft.Resources/deployments/registry-reference"
	for _, tt := range []struct {
		name, kind, state, wantError string
		nested, different, duplicate bool
		readBefore, readAfter        bool
		readOnlyNested, paginated    bool
	}{
		{name: "exact creation", kind: "Create", state: "Succeeded"},
		{name: "nested creation", kind: "Create", state: "Succeeded", nested: true},
		{name: "read before creation", kind: "Create", state: "Succeeded", readBefore: true},
		{name: "read after creation", kind: "Create", state: "Succeeded", readAfter: true},
		{name: "read before nested creation", kind: "Create", state: "Succeeded", nested: true, readBefore: true},
		{name: "read after nested creation", kind: "Create", state: "Succeeded", nested: true, readAfter: true},
		{name: "read-only nested deployment", kind: "Create", state: "Succeeded", readOnlyNested: true},
		{name: "read on previous page", kind: "Create", state: "Succeeded", paginated: true},
		{name: "outputs only", wantError: "cannot establish ownership"},
		{name: "read existing registry", kind: "Read", state: "Succeeded",
			wantError: "no successful registry creation operation"},
		{name: "read-only nested history", kind: "Read", state: "Succeeded", readOnlyNested: true,
			wantError: "no successful registry creation operation"},
		{name: "failed creation", kind: "Create", state: "Failed", wantError: "no successful registry creation operation"},
		{name: "read before failed creation", kind: "Create", state: "Failed", readBefore: true,
			wantError: "no successful registry creation operation"},
		{name: "untyped operation", state: "Succeeded", wantError: "no successful registry creation operation"},
		{name: "other registry", kind: "Create", state: "Succeeded", different: true, wantError: "cannot establish ownership"},
		{name: "ambiguous creation", kind: "Create", state: "Succeeded", duplicate: true,
			wantError: "multiple registry creation operations"},
		{name: "unavailable history", wantError: "verify previous registry ownership"},
	} {
		for _, action := range []string{"recover", "down"} {
			t.Run(tt.name+"/"+action, func(t *testing.T) {
				p, env := acrTestProvider(t)
				p.rgExplicit = true
				p.credential = acrOfflineCredential{}
				p.synthesisEnv = map[string]string{
					envKeySubscriptionID: "sub", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registryID,
				}
				if action == "down" && tt.wantError != "" {
					p.acrPending = acrTestPending()
					require.NoError(t, p.writeACRConfig(t.Context(), acrPendingConfigKey, p.acrPending))
				}
				beforeConfig, pending := maps.Clone(env.config), p.acrPending
				rootID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
				groupDeleted, historyDeleted := false, false
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
					Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
						require.Empty(t, tt.wantError, "unverified ownership must fail before destructive clients")
						require.Equal(t, "down", action)
						if request.Method == http.MethodDelete {
							require.True(t, strings.EqualFold("/subscriptions/sub/resourceGroups/foundry", request.URL.Path))
							groupDeleted = true
							return acrTestHTTPReply(t, request, http.StatusOK, nil)
						}
						require.Equal(t, http.MethodGet, request.Method)
						require.True(t, strings.HasSuffix(request.URL.Path, "/Microsoft.CognitiveServices/accounts"))
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"value": []any{}})
					}),
				}}
				if action == "down" && tt.wantError == "" {
					p.credential = acrTestCredential{}
				}
				p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
					if method == http.MethodDelete {
						require.Empty(t, tt.wantError)
						require.Equal(t, "down", action)
						require.Equal(t, rootID, path)
						assert.True(t, groupDeleted)
						historyDeleted = true
						return nil
					}
					require.Equal(t, http.MethodGet, method, "unverified ownership must not mutate resources or grants")
					switch path {
					case rootID:
						return acrTestReply(out, map[string]any{"properties": map[string]any{
							"provisioningState": "Succeeded",
							"outputs": map[string]any{
								"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": registryID},
								"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
							},
							"parameters": map[string]any{"resourceTokenSalt": map[string]any{"value": "salt"}},
						}})
					case testACRProject:
						return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
					case rootID + "/operations", nestedID + "/operations", referenceID + "/operations",
						rootID + "/operations?after=reads":
						if tt.name == "outputs only" {
							return &azcore.ResponseError{StatusCode: http.StatusNotFound}
						}
						if tt.name == "unavailable history" {
							return &azcore.ResponseError{StatusCode: http.StatusForbidden}
						}
						id, resourceType := registryID, "Microsoft.ContainerRegistry/registries"
						if tt.nested && path == rootID+"/operations" {
							id, resourceType = nestedID, "Microsoft.Resources/deployments"
						} else if tt.different {
							id += "other"
						}
						operation := map[string]any{"properties": map[string]any{
							"targetResource":        map[string]any{"id": id, "resourceType": resourceType},
							"provisioningOperation": tt.kind, "provisioningState": tt.state,
						}}
						read := map[string]any{"properties": map[string]any{
							"targetResource": map[string]any{
								"id": registryID, "resourceType": "Microsoft.ContainerRegistry/registries",
							},
							"provisioningOperation": "Read", "provisioningState": "Succeeded",
						}}
						if path == referenceID+"/operations" {
							return acrTestReply(out, map[string]any{"value": []any{read}})
						}
						if tt.paginated && path == rootID+"/operations" {
							return acrTestReply(out, map[string]any{
								"value": []any{read}, "nextLink": rootID + "/operations?after=reads",
							})
						}
						operations := []any{operation}
						if tt.readBefore {
							operations = append([]any{read}, operations...)
						}
						if tt.readAfter {
							operations = append(operations, read)
						}
						if tt.readOnlyNested && path == rootID+"/operations" {
							operations = append([]any{map[string]any{"properties": map[string]any{
								"targetResource": map[string]any{
									"id": referenceID, "resourceType": "Microsoft.Resources/deployments",
								},
								"provisioningOperation": "Create", "provisioningState": "Succeeded",
							}}}, operations...)
						}
						if tt.duplicate {
							operations = append(operations, operation)
						}
						return acrTestReply(out, map[string]any{"value": operations})
					default:
						t.Fatalf("unexpected recovery request: %s %s", method, path)
						return nil
					}
				}
				var err error
				if action == "recover" {
					err = p.recoverDeployedACRBinding(t.Context())
				} else {
					_, err = p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
				}
				if tt.wantError != "" {
					require.ErrorContains(t, err, tt.wantError)
					assert.Nil(t, p.acrBinding)
					assert.False(t, p.acrBindingRecovered)
					assert.False(t, groupDeleted)
					assert.False(t, historyDeleted)
				} else if action == "down" {
					require.NoError(t, err)
					assert.True(t, groupDeleted)
					assert.True(t, historyDeleted)
					assert.Nil(t, p.acrBinding)
					assert.Empty(t, env.config)
				} else {
					require.NoError(t, err)
					require.NotNil(t, p.acrBinding)
					assert.Equal(t, "create", p.acrBinding.Mode)
					assert.Equal(t, registryID, p.acrBinding.RegistryResourceID)
					assert.Equal(t, testACRProject, p.acrBinding.ProjectResourceID)
					assert.Equal(t, "salt", p.acrBinding.DeploymentSalt)
					assert.True(t, p.acrBindingRecovered)
				}
				assert.Same(t, pending, p.acrPending)
				if action != "down" || tt.wantError != "" {
					assert.Equal(t, beforeConfig, env.config)
				}
				assert.Empty(t, env.set)
			})
		}
	}
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
		{name: "read before partial creation", pending: true, wantMode: "create"},
		{name: "read after partial creation", pending: true, wantMode: "create"},
		{name: "canceled deployment", pending: true, wantMode: "create"},
		{name: "paginated evidence", wantMode: "create"},
		{name: "external other group", pending: true, wantMode: "reuse-connect"},
		{name: "external same group", pending: true, wantError: "no successful registry creation operation"},
		{name: "missing evidence", pending: true, wantError: "cannot establish ownership"},
		{name: "missing history", wantError: "cannot establish ownership"},
		{name: "missing evidence page", wantError: "verify partially deployed registry ownership"},
		{name: "repeated evidence page", wantError: "pagination repeated a page"},
		{name: "missing operation kind", wantError: "no successful registry creation operation"},
		{name: "failed creation", wantError: "no successful registry creation operation"},
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
						read := operation(registryID, "Microsoft.ContainerRegistry/registries", "Read", "Succeeded")
						if tt.name == "read before partial creation" {
							operations = append([]any{read}, operations...)
						}
						if tt.name == "read after partial creation" {
							operations = append(operations, read)
						}
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

func TestACRAccessRecoveryContractBeforeWrites(t *testing.T) {
	for _, tt := range []struct {
		name                      string
		include, owned, wantError bool
	}{
		{name: "new access", include: true, wantError: true},
		{name: "repeated access", include: true, owned: true, wantError: true},
		{name: "project-only retained grants", owned: true, wantError: true},
		{name: "unused selector"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, env := acrTestProvider(t)
			source := acrTestSource()
			declared, ok := source.armTemplate["parameters"].(map[string]any)
			require.True(t, ok)
			delete(declared, "acrOwnedAssignments")
			source.parameters["includeAcr"] = map[string]any{"value": tt.include}
			if tt.owned {
				p.acrBinding = acrTestBinding()
				p.acrBinding.OwnedAssignments = []synthesis.ACRAssignment{{
					ID: acrTestAssignment().ID, PrincipalID: "principal", OwnershipToken: testACRToken,
					RoleDefinitionID: acrTestPending().RoleDefinitionID,
					DeploymentIDs:    acrAccessDeploymentIDs(testACRID, testACRToken),
				}}
				if !tt.include {
					source.parameters["existingAcrResourceId"] = map[string]any{"value": ""}
				}
			}
			p.acrRequest = func(context.Context, string, string, string, any) error {
				t.Fatal("contract validation must precede cloud requests")
				return nil
			}
			err := p.prepareACRAccess(t.Context(), source)
			if tt.wantError {
				require.ErrorContains(t, err, "access contract is outdated")
				require.ErrorContains(t, err, "re-eject")
			} else {
				require.NoError(t, err)
			}
			assert.Nil(t, p.acrPending)
			assert.Empty(t, env.config)
			assert.Empty(t, env.set)
		})
	}
}

func TestACRRepeatProvisionThenStateLossCleanup(t *testing.T) {
	for _, tt := range []struct {
		name                                           string
		abac, changeMode, noAcr, missingGrant, managed bool
	}{
		{name: "classic"},
		{name: "ABAC", abac: true},
		{name: "permission mode change", changeMode: true},
		{name: "project-only repeat", noAcr: true},
		{name: "managed project-only repeat", noAcr: true, managed: true},
		{name: "already deleted grant", missingGrant: true},
	} {
		for _, layer := range []bool{false, true} {
			t.Run(tt.name+"/layer="+strconv.FormatBool(layer), func(t *testing.T) {
				p, env := acrTestProvider(t)
				p.projectPath = t.TempDir()
				p.rgExplicit, p.isLayer = true, layer
				p.location = "eastus2"
				p.credential = acrTestCredential{}
				group := "foundry"
				if layer {
					group = "on-disk-foundry"
					p.projectYAML = []byte("name: test\ninfra:\n  layers:\n" +
						"    - name: foundry\n      provider: microsoft.foundry\n")
				}
				groupID := "/subscriptions/sub/resourceGroups/" + group
				projectID := strings.Replace(testACRProject, "/foundry/", "/"+group+"/", 1)
				source := acrTestSource()
				declared, ok := source.armTemplate["parameters"].(map[string]any)
				require.True(t, ok)
				for _, name := range []string{"includeAcr", "resourceGroupName", "foundryProjectName"} {
					declared[name] = map[string]any{}
				}
				source.mode = templateModeBicep
				source.parameters["resourceGroupName"] = map[string]any{"value": group}
				registryID := testACRID
				if tt.managed {
					registryID = strings.Replace(testACRID, "/shared/", "/"+group+"/", 1)
					source.parameters["existingAcrResourceId"] = map[string]any{"value": ""}
				}
				p.onDiskSource = source
				rootID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
				creatorID := groupID + "/providers/Microsoft.Resources/deployments/acr"
				groupExists, rootExists := false, false
				abac := tt.abac
				var submitted map[string]struct{ Value any }
				var rootOutputs map[string]any
				var rootTags map[string]string
				accessDeployments := map[string]map[string]any{}
				administrator := acrTestAssignment()
				administrator.ID += "-administrator"
				administrator.Properties.PrincipalID = "administrator"
				administrator.Properties.Description = ""
				assignments := map[string]acrRoleAssignment{administrator.ID: administrator}
				groupLookups, provisions := 0, 0
				transport := acrTestTransport(func(request *http.Request) (*http.Response, error) {
					reply := func(code int, value any) (*http.Response, error) {
						return acrTestHTTPReply(t, request, code, value)
					}
					path := request.URL.Path
					switch {
					case request.Method == http.MethodGet && path == rootID+"/operations":
						require.True(t, tt.managed)
						return reply(http.StatusOK, map[string]any{"value": []any{map[string]any{
							"properties": map[string]any{
								"targetResource": map[string]any{
									"id": creatorID, "resourceType": "Microsoft.Resources/deployments",
								},
								"provisioningOperation": "Create", "provisioningState": "Succeeded",
							},
						}}})
					case request.Method == http.MethodGet && path == creatorID+"/operations":
						require.True(t, tt.managed)
						return reply(http.StatusOK, map[string]any{"value": []any{map[string]any{
							"properties": map[string]any{
								"targetResource": map[string]any{
									"id": registryID, "resourceType": "Microsoft.ContainerRegistry/registries",
								},
								"provisioningOperation": "Create", "provisioningState": "Succeeded",
							},
						}}})
					case request.Method == http.MethodGet && strings.EqualFold(path, groupID):
						groupLookups++
						if !groupExists {
							return reply(http.StatusNotFound, nil)
						}
						return reply(http.StatusOK, map[string]any{
							"id": groupID, "tags": map[string]string{"azd-env-name": p.envName},
						})
					case request.Method == http.MethodGet && path == projectID:
						return reply(http.StatusOK, map[string]any{"identity": map[string]any{"principalId": "principal"}})
					case request.Method == http.MethodGet && path == testACRID:
						mode := "LegacyRegistryPermissions"
						if abac {
							mode = "AbacRepositoryPermissions"
						}
						return reply(http.StatusOK, map[string]any{"properties": map[string]any{"roleAssignmentMode": mode}})
					case request.Method == http.MethodGet && strings.HasSuffix(path, "/roleAssignments"):
						return reply(http.StatusOK, map[string]any{"value": slices.Collect(maps.Values(assignments))})
					case strings.Contains(path, "/roleAssignments/"):
						assignment, found := assignments[path]
						if !found {
							return reply(http.StatusNotFound, nil)
						}
						if request.Method == http.MethodDelete {
							require.NotEqual(t, administrator.ID, path)
							delete(assignments, path)
							return reply(http.StatusNoContent, nil)
						}
						require.Equal(t, http.MethodGet, request.Method)
						return reply(http.StatusOK, assignment)
					case path == rootID && request.Method == http.MethodPut:
						var deployment struct {
							Tags       map[string]string
							Properties struct {
								Parameters map[string]struct{ Value any }
							}
						}
						require.NoError(t, json.NewDecoder(request.Body).Decode(&deployment))
						submitted = deployment.Properties.Parameters
						rootTags = deployment.Tags
						assert.Equal(t, group, submitted["resourceGroupName"].Value)
						include, _ := submitted["includeAcr"].Value.(bool)
						token, _ := submitted["acrAccessOperationId"].Value.(string)
						assigned, _ := submitted["acrPullAssigned"].Value.(bool)
						if include && !assigned && !tt.managed {
							require.NotEmpty(t, token)
							assignment := acrTestAssignment()
							assignment.ID = testACRID + "/providers/Microsoft.Authorization/roleAssignments/" + token
							assignment.Properties.Description = "azd-foundry:" + token
							assignment.Properties.RoleDefinitionID, ok = submitted["acrPullRoleDefinitionId"].Value.(string)
							require.True(t, ok)
							assignments[assignment.ID] = assignment
							ids := acrAccessDeploymentIDs(testACRID, token)
							accessDeployments[ids[0]] = map[string]any{
								"assignmentName":       map[string]any{"value": token},
								"ownershipDescription": map[string]any{"value": "azd-foundry:" + token},
							}
							accessDeployments[ids[1]] = map[string]any{
								"accessOperationId": map[string]any{"value": token},
							}
						}
						outputRegistryID := registryID
						if !include {
							outputRegistryID = ""
						}
						rootOutputs = map[string]any{
							"AZURE_AI_PROJECT_ID":                  map[string]any{"type": "string", "value": projectID},
							"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"type": "string", "value": outputRegistryID},
							envKeyFoundryRGOwner:                   map[string]any{"type": "string", "value": groupID},
						}
						groupExists, rootExists = true, true
						provisions++
						return reply(http.StatusOK, map[string]any{"properties": map[string]any{
							"provisioningState": "Succeeded", "outputs": rootOutputs,
							"outputResources": []map[string]any{{"id": groupID}},
						}})
					case path == rootID && request.Method == http.MethodGet:
						if !rootExists {
							return reply(http.StatusNotFound, nil)
						}
						return reply(http.StatusOK, map[string]any{
							"tags": rootTags, "properties": map[string]any{
								"provisioningState": "Succeeded", "parameters": submitted, "outputs": rootOutputs,
							},
						})
					case strings.Contains(path, "/providers/Microsoft.Resources/deployments/"):
						if path == rootID {
							require.Equal(t, http.MethodDelete, request.Method)
							require.False(t, groupExists)
							require.Empty(t, accessDeployments)
							rootExists = false
							return reply(http.StatusNoContent, nil)
						}
						parameters, found := accessDeployments[path]
						if !found {
							return reply(http.StatusNotFound, nil)
						}
						if request.Method == http.MethodDelete {
							delete(accessDeployments, path)
							return reply(http.StatusNoContent, nil)
						}
						require.Equal(t, http.MethodGet, request.Method)
						return reply(http.StatusOK, map[string]any{"properties": map[string]any{
							"provisioningState": "Succeeded", "parameters": parameters,
						}})
					case request.Method == http.MethodGet && strings.HasSuffix(path, "/Microsoft.CognitiveServices/accounts"):
						require.True(t, strings.HasPrefix(strings.ToLower(path), strings.ToLower(groupID)+"/"))
						return reply(http.StatusOK, map[string]any{"value": []any{}})
					case request.Method == http.MethodDelete && strings.EqualFold(path, groupID):
						require.Len(t, assignments, 1)
						require.Empty(t, accessDeployments)
						groupExists = false
						return reply(http.StatusOK, nil)
					default:
						t.Fatalf("unexpected ARM request: %s %s", request.Method, path)
						return nil, errors.New("unexpected ARM request")
					}
				})
				p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{Transport: transport}}
				repeats := 2
				if tt.changeMode {
					repeats = 3
				}
				var deployed *azdext.ProvisioningDeployResult
				for i := range repeats {
					if i > 0 {
						abac = tt.abac || tt.changeMode
						if tt.noAcr {
							source.parameters["includeAcr"] = map[string]any{"value": false}
							source.parameters["existingAcrResourceId"] = map[string]any{"value": ""}
						}
					}
					var err error
					deployed, err = p.Deploy(t.Context(), func(string) {})
					require.NoError(t, err)
					require.NotNil(t, deployed)
					assert.Equal(t, group, p.rgName)
					if !tt.managed || layer {
						assert.Equal(t, groupID, p.foundryRGOwnerID)
					}
					require.Contains(t, deployed.Deployment.Outputs, envKeyFoundryRGOwner)
					assert.Equal(t, groupID, deployed.Deployment.Outputs[envKeyFoundryRGOwner].Value)
				}
				assert.Equal(t, repeats, provisions)
				if !tt.managed || layer {
					assert.Positive(t, groupLookups)
				}
				assert.Empty(t, submitted["acrAccessOperationId"].Value)
				wantOwned := 1
				if tt.managed {
					wantOwned = 0
					require.Equal(t, "registry", rootTags[acrManagedRegistryTag])
					require.Equal(t, "acr", rootTags[acrManagedDeploymentTag])
				}
				if tt.changeMode {
					wantOwned = 2
				}
				owned, err := retainedACRAssignments(submitted)
				require.NoError(t, err)
				require.Len(t, owned, wantOwned)
				if tt.missingGrant {
					delete(assignments, owned[0].ID)
				}
				delete(env.config, synthesis.ACRBindingConfigKey)
				delete(env.config, acrPendingConfigKey)
				next := &FoundryProvisioningProvider{
					azdClient: p.azdClient, projectPath: p.projectPath, projectYAML: p.projectYAML,
					envName: p.envName, subID: p.subID, rgName: group, rgExplicit: true,
					isLayer: layer, foundryRGOwnerID: deployed.Deployment.Outputs[envKeyFoundryRGOwner].Value,
					location: p.location, foundryName: p.foundryName,
					credential: p.credential, armClientOptions: p.armClientOptions,
				}
				if tt.managed {
					next.synthesisEnv = map[string]string{
						"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": group,
						"AZURE_AI_PROJECT_ID": projectID, "AZD_FOUNDRY_ACR_MODE": "create",
						"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": registryID,
						"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "registry.azurecr.io",
					}
				}
				result, err := next.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.False(t, rootExists)
				assert.False(t, groupExists)
				assert.Empty(t, accessDeployments)
				assert.Equal(t, map[string]acrRoleAssignment{administrator.ID: administrator}, assignments)
				assert.Empty(t, env.config)
			})
		}
	}
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

func TestACRRetainedRegistryChecksApplicationScope(t *testing.T) {
	for _, action := range []string{
		"project-only provision", "down", "down after state loss", "down after partial state loss",
	} {
		t.Run(action, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.rgExplicit = true
			p.credential = acrOfflineCredential{}
			p.projectYAML = []byte("name: test\ninfra:\n  provider: bicep\n  layers:\n" +
				"    - name: application\n    - name: foundry\n      provider: microsoft.foundry\n")
			p.synthesisEnv = map[string]string{
				"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_RESOURCE_GROUP": "shared",
				"AZURE_FOUNDRY_RESOURCE_GROUP": "foundry",
			}
			recovering := strings.Contains(action, "state loss")
			if !recovering {
				p.acrBinding = acrTestBinding()
			}
			reads := 0
			rootID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				require.True(t, recovering)
				require.Equal(t, http.MethodGet, method)
				reads++
				switch path {
				case rootID:
					if action == "down after partial state loss" {
						return acrTestReply(out, map[string]any{"properties": map[string]any{
							"provisioningState": "Failed",
						}})
					}
					return acrTestReply(out, map[string]any{"properties": map[string]any{
						"provisioningState": "Succeeded",
						"parameters":        map[string]any{"existingAcrResourceId": map[string]any{"value": testACRID}},
						"outputs": map[string]any{
							"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": testACRID},
							"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
						},
					}})
				case "/subscriptions/sub/resourceGroups/foundry/" +
					"providers/Microsoft.Resources/deployments/foundry-resources/operations":
					return acrTestReply(out, map[string]any{"value": []any{map[string]any{"properties": map[string]any{
						"targetResource": map[string]any{
							"id": testACRProject, "resourceType": "Microsoft.CognitiveServices/accounts/projects",
						},
					}}}})
				case testACRProject:
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				case testACRProject + "/connections":
					return acrTestReply(out, map[string]any{"value": []any{map[string]any{"properties": map[string]any{
						"category": "ContainerRegistry", "metadata": map[string]string{"ResourceId": testACRID},
					}}}})
				default:
					t.Fatalf("unexpected recovery request: %s %s", method, path)
					return nil
				}
			}
			var err error
			if action == "project-only provision" {
				source := acrTestSource()
				source.parameters["includeAcr"] = map[string]any{"value": false}
				source.parameters["existingAcrResourceId"] = map[string]any{"value": ""}
				err = p.validateACRTemplate(source)
			} else {
				_, err = p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			}
			require.ErrorContains(t, err, "azd down deletes")
			if recovering {
				wantReads := 1
				if action == "down after partial state loss" {
					wantReads = 4
				}
				assert.Equal(t, wantReads, reads)
				assert.Nil(t, p.acrBinding)
			} else {
				assert.Zero(t, reads)
			}
			assert.Empty(t, env.config)
			assert.Empty(t, env.set)
		})
	}
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
		mode               string
		down, refresh      bool
		agentBeforeRefresh bool
	}{
		{mode: ""}, {mode: "reuse-connect"},
		{mode: "", down: true}, {mode: "reuse-connect", down: true},
		{mode: "", refresh: true}, {mode: "reuse-connect", refresh: true},
		{mode: "", down: true, refresh: true}, {mode: "reuse-connect", down: true, refresh: true},
		{mode: "", refresh: true, agentBeforeRefresh: true},
		{mode: "reuse-connect", refresh: true, agentBeforeRefresh: true},
		{mode: "", down: true, refresh: true, agentBeforeRefresh: true},
		{mode: "reuse-connect", down: true, refresh: true, agentBeforeRefresh: true},
	} {
		name := fmt.Sprintf("%s/down=%t/refresh=%t/agentBeforeRefresh=%t",
			tt.mode, tt.down, tt.refresh, tt.agentBeforeRefresh)
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			manifest := "services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n"
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
			var deployedOutputs map[string]any
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
						deployedOutputs = nil
						return acrTestHTTPReply(t, request, http.StatusNoContent, nil)
					}
					require.Contains(t, request.URL.Path, "/providers/Microsoft.Resources/deployments/")
					if request.Method == http.MethodGet {
						if deployedOutputs == nil {
							return acrTestHTTPReply(t, request, http.StatusNotFound, nil)
						}
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
							"properties": map[string]any{"provisioningState": "Succeeded", "outputs": deployedOutputs},
						})
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
					deployedOutputs = outputs
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

			agentManifest := manifest + "  agent:\n    host: azure.ai.agent\n    kind: hosted\n"
			if tt.agentBeforeRefresh {
				require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(agentManifest), 0o600))
			}
			if tt.refresh {
				refresher := &FoundryProvisioningProvider{
					azdClient: p.azdClient, credential: p.credential, armClientOptions: p.armClientOptions,
				}
				require.NoError(t, refresher.Initialize(t.Context(), root,
					&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
				expectedMode := "none"
				if tt.agentBeforeRefresh {
					expectedMode = "reuse-connect"
				}
				require.Equal(t, expectedMode, refresher.greenfieldAcrMode)
				env.configErr = errors.New("refresh must not persist registry lifecycle state")
				state, err := refresher.State(t.Context(), &azdext.ProvisioningStateOptions{})
				env.configErr = nil
				require.NoError(t, err)
				require.NotNil(t, state.State)
				assert.Empty(t, env.config)
				assert.Empty(t, env.set)
				for key, output := range state.State.Outputs {
					values[key] = output.Value
				}
				require.NoError(t, godotenv.Write(values, envPath))
				env.get, err = godotenv.Read(envPath)
				require.NoError(t, err)
				require.Equal(t, testACRID, env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
				assert.Equal(t, tt.mode, env.get["AZD_FOUNDRY_ACR_MODE"])
			}

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

			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(agentManifest), 0o600))
			next := &FoundryProvisioningProvider{azdClient: p.azdClient}
			require.NoError(t, next.Initialize(t.Context(), root,
				&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
			assert.Equal(t, "reuse-connect", next.greenfieldAcrMode)
			assert.Equal(t, map[string]any{"value": testACRID}, next.armParameters()["existingAcrResourceId"])
		})
	}
}

func TestACROutputPreservationKeepsCurrentOutputs(t *testing.T) {
	for _, tt := range []struct {
		name, registry, endpoint, connection string
		brownfield                           bool
	}{
		{
			name: "current registry", registry: testACRID,
			endpoint: "current.azurecr.io", connection: "current-connection",
		},
		{
			name: "current connection", endpoint: "current.azurecr.io", connection: "current-connection",
		},
		{name: "brownfield", brownfield: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.acrBinding = acrTestBinding()
			if tt.brownfield {
				p.brownfieldEndpoint = "https://existing.services.ai.azure.com/api/projects/project"
			}
			env.getErr = map[string]error{
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    errors.New("must not replace current outputs"),
				"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": errors.New("must not replace current outputs"),
			}
			outputs := map[string]*azdext.ProvisioningOutputParameter{
				"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": {Type: "string", Value: tt.registry},
				"AZURE_CONTAINER_REGISTRY_ENDPOINT":    {Type: "string", Value: tt.endpoint},
				"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": {Type: "string", Value: tt.connection},
			}
			require.NoError(t, p.preserveACROutputs(t.Context(), outputs))
			registry := tt.registry
			if !tt.brownfield {
				registry = testACRID
			}
			assert.Equal(t, registry, outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"].Value)
			assert.Equal(t, tt.endpoint, outputs["AZURE_CONTAINER_REGISTRY_ENDPOINT"].Value)
			assert.Equal(t, tt.connection, outputs["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"].Value)
			assert.Empty(t, env.set)
			assert.Empty(t, env.config)
		})
	}
}

func TestACRStatePreservesDeployedRegistryOutputs(t *testing.T) {
	for _, mode := range []string{"create", "reuse-connect"} {
		for _, alias := range []bool{false, true} {
			for _, failRead := range []string{"", "AZURE_CONTAINER_REGISTRY_ENDPOINT", "AZURE_AI_PROJECT_ACR_CONNECTION_NAME"} {
				t.Run(fmt.Sprintf("%s/alias=%t/read=%s", mode, alias, failRead), func(t *testing.T) {
					root := t.TempDir()
					require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte(
						"services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n"), 0o600))
					p, env := acrTestProvider(t)
					binding := acrTestBinding()
					binding.Mode = mode
					if mode == "create" {
						binding.RegistryResourceID = strings.Replace(testACRID, "/shared/", "/foundry/", 1)
					}
					data, err := json.Marshal(binding)
					require.NoError(t, err)
					env.config = map[string][]byte{synthesis.ACRBindingConfigKey: data}
					env.get = map[string]string{
						envKeySubscriptionID: "sub", envKeyLocation: "eastus2",
						envKeyResourceGroup: "foundry", envKeyProjectName: "project",
						"AZD_FOUNDRY_ACR_MODE":                 mode,
						"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "registry.azurecr.io",
						"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "registry-connection",
					}
					if alias {
						env.get["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = binding.RegistryResourceID
					}
					require.NoError(t, p.Initialize(t.Context(), root,
						&azdext.ProvisioningOptions{Provider: FoundryProviderName}))
					beforeValues, beforeConfig := maps.Clone(env.get), maps.Clone(env.config)
					env.configErr = errors.New("state must not persist registry lifecycle state")
					if failRead != "" {
						env.getErr = map[string]error{failRead: errors.New("registry output unavailable")}
					}
					p.credential = acrTestCredential{}
					reads := 0
					p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
						Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
							require.Equal(t, http.MethodGet, request.Method)
							require.Equal(t, "/subscriptions/sub/providers/Microsoft.Resources/deployments/"+
								p.deploymentName(), request.URL.Path)
							reads++
							outputs := map[string]any{"AZURE_AI_PROJECT_ID": map[string]any{
								"type": "string", "value": testACRProject,
							}}
							for _, key := range []string{
								"AZURE_CONTAINER_REGISTRY_RESOURCE_ID", "AZURE_CONTAINER_REGISTRY_ENDPOINT",
								"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
							} {
								outputs[key] = map[string]any{"type": "string", "value": ""}
							}
							return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{
								"properties": map[string]any{"provisioningState": "Succeeded", "outputs": outputs},
							})
						}),
					}}
					state, err := p.State(t.Context(), &azdext.ProvisioningStateOptions{})
					if failRead != "" {
						require.ErrorContains(t, err, "preserve deployed registry output")
						require.ErrorContains(t, err, "registry output unavailable")
						assert.Nil(t, state)
					} else {
						require.NoError(t, err)
						require.NotNil(t, state.State)
						for key, value := range map[string]string{
							"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": binding.RegistryResourceID,
							"AZURE_CONTAINER_REGISTRY_ENDPOINT":    "registry.azurecr.io",
							"AZURE_AI_PROJECT_ACR_CONNECTION_NAME": "registry-connection",
						} {
							require.Contains(t, state.State.Outputs, key)
							assert.Equal(t, value, state.State.Outputs[key].Value)
						}
					}
					assert.Equal(t, 1, reads)
					assert.Equal(t, beforeValues, env.get)
					assert.Equal(t, beforeConfig, env.config)
					assert.Empty(t, env.set)
					assert.Equal(t, binding, p.acrBinding)
					assert.Nil(t, p.acrPending)
				})
			}
		}
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
				"services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n"+
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
		name                                                 string
		history                                              string
		abac, granted, pending, newProject, embedded, casing bool
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
		{name: "on-disk alternate declarations", casing: true},
		{name: "on-disk alternate declarations with ABAC access", casing: true, abac: true, granted: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.projectPath = t.TempDir()
			p.location = "eastus2"
			p.credential = acrTestCredential{}
			p.onDiskSource = acrTestSource()
			if tt.casing {
				declared, ok := p.onDiskSource.armTemplate["parameters"].(map[string]any)
				require.True(t, ok)
				renamed := map[string]any{}
				for name, value := range declared {
					renamed[strings.ToUpper(name)] = value
				}
				p.onDiskSource.armTemplate["parameters"] = renamed
				compiled, err := json.Marshal(p.onDiskSource.armTemplate)
				require.NoError(t, err)
				params := minimalARMParametersFile(t, map[string]any{
					"includeAcr": true, "existingAcrResourceId": testACRID,
				})
				infraDir := filepath.Join(p.projectPath, onDiskInfraDir)
				require.NoError(t, os.MkdirAll(infraDir, 0o750))
				require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("// bicep\n"), 0o600))
				require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskParamsFile), []byte(params), 0o600))
				p.bicepCliInstance = &stubCompiler{buildResult: bicep.BuildResult{Compiled: string(compiled)}}
				p.onDiskSource = nil
			}
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
				case testACRProject + "/connections":
					return acrTestReply(out, map[string]any{"value": []any{}})
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
					case request.Method == http.MethodGet && strings.HasSuffix(path, "/roleAssignments"):
						require.True(t, created)
						assignment := acrTestAssignment()
						assignment.ID = testACRID + "/providers/Microsoft.Authorization/roleAssignments/" + operationID
						assignment.Properties.Description = "azd-foundry:" + operationID
						return reply(http.StatusOK, map[string]any{"value": []acrRoleAssignment{assignment}})
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

func TestACRRecoveredZeroGrantBindingSurvivesDestroyRetry(t *testing.T) {
	for _, mode := range []string{"create", "reuse-connect"} {
		t.Run(mode, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.rgExplicit = true
			p.credential = acrTestCredential{}
			registryID := testACRID
			if mode == "create" {
				registryID = strings.Replace(registryID, "/shared/", "/foundry/", 1)
			}
			deploymentID := "/subscriptions/sub/providers/Microsoft.Resources/deployments/" + p.deploymentName()
			groupID := "/subscriptions/sub/resourceGroups/foundry"
			groupGone, failRetirement := false, true
			projectReads, groupDeletes := 0, 0
			p.armClientOptions = &arm.ClientOptions{ClientOptions: policy.ClientOptions{
				Retry: policy.RetryOptions{MaxRetries: -1},
				Transport: acrTestTransport(func(request *http.Request) (*http.Response, error) {
					path := request.URL.Path
					switch {
					case request.Method == http.MethodGet && strings.HasSuffix(path, "/Microsoft.CognitiveServices/accounts"):
						return acrTestHTTPReply(t, request, http.StatusOK, map[string]any{"value": []any{}})
					case request.Method == http.MethodDelete && strings.EqualFold(path, groupID):
						require.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey],
							"recovered bindings must be durable before resource deletion")
						groupDeletes++
						if groupGone {
							return acrTestHTTPReply(t, request, http.StatusNotFound, nil)
						}
						groupGone = true
						return acrTestHTTPReply(t, request, http.StatusOK, nil)
					case request.Method == http.MethodGet && strings.EqualFold(path, groupID):
						require.True(t, groupGone)
						return acrTestHTTPReply(t, request, http.StatusNotFound, nil)
					default:
						t.Fatalf("unexpected resource-group request: %s %s", request.Method, path)
						return nil, errors.New("unexpected resource-group request")
					}
				}),
			}}
			p.acrRequest = func(_ context.Context, method, path, _ string, out any) error {
				switch {
				case method == http.MethodGet && path == deploymentID:
					parameters := map[string]any{}
					if mode == "reuse-connect" {
						parameters["existingAcrResourceId"] = map[string]any{"value": registryID}
					}
					return acrTestReply(out, map[string]any{"properties": map[string]any{
						"provisioningState": "Succeeded", "parameters": parameters,
						"outputs": map[string]any{
							"AZURE_CONTAINER_REGISTRY_RESOURCE_ID": map[string]any{"value": registryID},
							"AZURE_AI_PROJECT_ID":                  map[string]any{"value": testACRProject},
						},
					}})
				case method == http.MethodGet && path == deploymentID+"/operations":
					require.Equal(t, "create", mode)
					return acrTestReply(out, map[string]any{"value": []any{map[string]any{
						"properties": map[string]any{
							"targetResource": map[string]any{
								"id": registryID, "resourceType": "Microsoft.ContainerRegistry/registries",
							},
							"provisioningOperation": "Create", "provisioningState": "Succeeded",
						},
					}}})
				case method == http.MethodGet && path == testACRProject:
					require.False(t, groupGone, "retry must not read the deleted project")
					projectReads++
					return acrTestReply(out, map[string]any{"identity": map[string]any{"principalId": "principal"}})
				case method == http.MethodDelete && path == deploymentID:
					require.True(t, groupGone)
					require.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey])
					if failRetirement {
						return &azcore.ResponseError{StatusCode: http.StatusForbidden}
					}
					return nil
				default:
					t.Fatalf("unexpected registry lifecycle request: %s %s", method, path)
					return errors.New("unexpected registry lifecycle request")
				}
			}
			require.Empty(t, env.config, "simulate local lifecycle state loss")
			result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.ErrorContains(t, err, "delete completed Foundry deployment record")
			require.Nil(t, result)
			require.True(t, groupGone)
			require.NotNil(t, p.acrBinding)
			require.Empty(t, p.acrBinding.OwnedAssignments)
			require.NotEmpty(t, env.config[synthesis.ACRBindingConfigKey])
			retry := &FoundryProvisioningProvider{
				azdClient: p.azdClient, envName: p.envName, subID: p.subID,
				rgName: p.rgName, foundryName: p.foundryName, rgExplicit: true,
				credential: p.credential, armClientOptions: p.armClientOptions, acrRequest: p.acrRequest,
			}
			require.NoError(t, retry.loadACRLifecycle(t.Context()))
			require.Equal(t, p.acrBinding, retry.acrBinding)
			failRetirement = false
			result, err = retry.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, func(string) {})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, 1, projectReads)
			assert.Equal(t, 2, groupDeletes)
			assert.Empty(t, env.config)
		})
	}
}

func TestACRRecoveredZeroGrantBindingMustPersistBeforeCleanup(t *testing.T) {
	for _, mode := range []string{"create", "reuse-connect"} {
		t.Run(mode, func(t *testing.T) {
			p, env := acrTestProvider(t)
			p.acrBinding = acrTestBinding()
			p.acrBinding.Mode = mode
			p.acrBindingRecovered = true
			env.configErr = errors.New("cannot save recovered binding")
			p.acrRequest = func(context.Context, string, string, string, any) error {
				t.Fatal("failed persistence must prevent cleanup requests")
				return nil
			}
			require.ErrorContains(t, p.cleanupACRAccess(t.Context()), "cannot save recovered binding")
			assert.True(t, p.acrBindingRecovered)
			assert.Empty(t, env.config)
		})
	}
}

func TestACRProjectOnlyEjectionRemainsDemandDriven(t *testing.T) {
	for _, layer := range []bool{false, true} {
		for _, addAgent := range []bool{false, true} {
			for _, explicitFalse := range []bool{false, true} {
				t.Run(strconv.FormatBool(layer)+"/agent="+strconv.FormatBool(addAgent)+
					"/override="+strconv.FormatBool(explicitFalse), func(t *testing.T) {
					p, _ := acrTestProvider(t)
					root := t.TempDir()
					path, module := "infra", "main"
					raw := "infra:\n  provider: microsoft.foundry\n"
					if layer {
						path, module = filepath.Join("infra", "foundry"), "project"
						raw = "infra:\n  layers:\n    - name: foundry\n      provider: microsoft.foundry\n" +
							"      path: infra/foundry\n      module: project\n"
					}
					raw += "services:\n  project:\n    host: azure.ai.project\n    monitoring: {enabled: false}\n"
					values := map[string]string{
						"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_CONTAINER_REGISTRY_RESOURCE_ID": testACRID,
					}
					input := synthesis.Input{
						RawAzureYAML: []byte(raw), ServiceName: "project", ProjectRoot: root,
						Env: values, PreserveVarRefs: true,
					}
					ejected, err := synthesis.Synthesize(input)
					require.NoError(t, err)
					require.NotContains(t, ejected.Parameters, "includeAcr")
					require.Equal(t, "${AZURE_CONTAINER_REGISTRY_RESOURCE_ID}", ejected.Parameters["existingAcrResourceId"])
					if explicitFalse {
						ejected.Parameters["includeAcr"] = false
					}
					params := minimalARMParametersFile(t, ejected.Parameters)
					infra := filepath.Join(root, path)
					require.NoError(t, os.MkdirAll(infra, 0o750))
					require.NoError(t, os.WriteFile(filepath.Join(infra, module+".bicep"), []byte("// test"), 0o600))
					paramsPath := filepath.Join(infra, module+".parameters.json")
					require.NoError(t, os.WriteFile(paramsPath, []byte(params), 0o600))
					if addAgent {
						raw += "  agent:\n    host: azure.ai.agent\n    kind: hosted\n"
					}
					input.RawAzureYAML, input.PreserveVarRefs = []byte(raw), false
					p.synthResult, err = synthesis.Synthesize(input)
					require.NoError(t, err)
					p.projectPath, p.projectYAML, p.synthesisEnv = root, input.RawAzureYAML, values
					p.infraPath, p.infraModule, p.isLayer = infra, module, layer
					template := acrTestSource().armTemplate
					declared, ok := template["parameters"].(map[string]any)
					require.True(t, ok)
					declared["includeAcr"] = map[string]any{"type": "bool", "defaultValue": false}
					compiled, err := json.Marshal(template)
					require.NoError(t, err)
					p.bicepCliInstance = &stubCompiler{buildResult: bicep.BuildResult{Compiled: string(compiled)}}
					source, err := p.resolveTemplate(t.Context(), func(string) {})
					require.NoError(t, err)
					assert.Equal(t, addAgent && !explicitFalse, parameterValue(source, "includeAcr"))
					assert.Equal(t, testACRID, parameterValue(source, "existingAcrResourceId"))
					//nolint:gosec // Test-owned path.
					after, err := os.ReadFile(paramsPath)
					require.NoError(t, err)
					assert.Equal(t, params, string(after), "provision must not rewrite authored parameters")
				})
			}
		}
	}
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
