// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
	assert.Equal(t, []string{acrTestPending().assignmentID()}, deleted)
	assert.Nil(t, p.acrPending)
	assert.NotContains(t, env.config, acrPendingConfigKey)
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
