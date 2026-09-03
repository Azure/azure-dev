// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestDelegatedProjectAddRequestValidation(t *testing.T) {
	request := &projectAddRequest{
		SchemaVersion: delegatedSchemaVersion,
		Source:        delegatedSourceAgents,
		SourceVersion: "1.0.0-beta.9",
		Project:       delegatedProject{ResourceID: "/subscriptions/s"},
	}
	require.NoError(t, request.validate())

	request.Project.Endpoint = "https://account.services.ai.azure.com/api/projects/p"
	require.Error(t, request.validate())
	request.Project.Endpoint = ""
	request.SchemaVersion = 2
	err := request.validate()
	require.Error(t, err)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, azdext.LocalErrorCategoryCompatibility, localErr.Category)
}

func TestDelegatedRequestRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	require.NoError(t, os.WriteFile(requestPath, []byte(`{
		"schemaVersion": 1,
		"source": "azure.ai.agents/init",
		"sourceVersion": "1.0.0",
		"unknown": true
	}`), 0600))
	request := &projectAddRequest{}
	require.Error(t, decodeDelegatedJSON(requestPath, request))
}

func TestDelegatedRequestIsInputOnly(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	require.NoError(t, os.WriteFile(requestPath, []byte(`{}`), 0600))
	root := NewRootCommand()
	addCommand, _, err := root.Find([]string{"add"})
	require.NoError(t, err)
	deploymentCommand, _, err := root.Find([]string{"deployment", "add"})
	require.NoError(t, err)
	assert.Nil(t, addCommand.Flags().Lookup("result-file"))
	assert.Nil(t, deploymentCommand.Flags().Lookup("result-file"))
	assert.NoError(t, validateDelegatedFilePath(requestPath, "request", true))
}

func TestProjectCommandsRegistered(t *testing.T) {
	root := NewRootCommand()
	addCommand, _, err := root.Find([]string{"add"})
	require.NoError(t, err)
	assert.Equal(t, "add", addCommand.Name())
	deploymentCommand, _, err := root.Find([]string{"deployment", "add"})
	require.NoError(t, err)
	assert.Equal(t, "add", deploymentCommand.Name())
	assert.True(t, addCommand.Flags().Lookup("request-file").Hidden)
	assert.True(t, deploymentCommand.Flags().Lookup("request-file").Hidden)
	assertOutputFlagOptions(t, addCommand, "default", []string{"default", "json", "none"})

	assert.Equal(t, "bicep", addCommand.Flags().Lookup("infra").NoOptDefVal)
	_, _, err = root.Find([]string{"init"})
	require.Error(t, err)
}

func TestProjectFileExists(t *testing.T) {
	root := t.TempDir()

	exists, err := projectFileExists(root)
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yml"), []byte("name: test\n"), 0600))
	exists, err = projectFileExists(root)
	require.NoError(t, err)
	assert.True(t, exists)

	require.NoError(t, os.Remove(filepath.Join(root, "azure.yml")))
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0600))
	exists, err = projectFileExists(root)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestResolvedProjectFromEndpoint(t *testing.T) {
	project, err := resolvedProjectFromEndpoint(
		"https://account.services.ai.azure.com/api/projects/foundry-project/",
	)
	require.NoError(t, err)
	assert.Equal(t, projectModeExistingEndpoint, project.Mode)
	assert.Equal(t, "account", project.AccountName)
	assert.Equal(t, "foundry-project", project.ProjectName)
	assert.Equal(
		t,
		"https://account.services.ai.azure.com/api/projects/foundry-project",
		project.Endpoint,
	)
}

func TestWriteTerraformEjectedInfra(t *testing.T) {
	for _, test := range []struct {
		name       string
		includeAcr bool
	}{
		{name: "without ACR", includeAcr: false},
		{name: "with ACR", includeAcr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			infraDir := filepath.Join(t.TempDir(), "infra")
			require.NoError(t, os.MkdirAll(infraDir, 0750))
			parameters := map[string]any{
				"includeAcr": test.includeAcr,
				"deployments": []synthesis.Deployment{{
					Name: "chat",
					Model: synthesis.DeploymentModel{
						Format:  "OpenAI",
						Name:    "gpt-4.1",
						Version: "2025-04-14",
					},
					Sku: synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
				}},
				"connections": []synthesis.Connection{{
					Name:     "search",
					Category: "CognitiveSearch",
					Target:   "https://search.example.com",
					AuthType: "ApiKey",
				}},
				"connectionCredentials": map[string]map[string]any{
					"search": {"key": "${SEARCH_API_KEY}"},
				},
			}

			require.NoError(t, writeTerraformEjectedInfra(infraDir, parameters))

			// #nosec G304
			outputs, err := os.ReadFile(filepath.Join(infraDir, "outputs.tf"))
			require.NoError(t, err)
			assert.Contains(t, string(outputs), "AZURE_AI_PROJECT_ID")
			if test.includeAcr {
				assert.Contains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_ENDPOINT")
				_, err := os.Stat(filepath.Join(infraDir, "container-registry.tf"))
				assert.NoError(t, err)
			} else {
				assert.NotContains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_ENDPOINT")
				_, err := os.Stat(filepath.Join(infraDir, "container-registry.tf"))
				assert.ErrorIs(t, err, os.ErrNotExist)
			}

			// #nosec G304
			rawTfvars, err := os.ReadFile(filepath.Join(infraDir, "main.tfvars.json"))
			require.NoError(t, err)
			tfvars := map[string]any{}
			require.NoError(t, json.Unmarshal(rawTfvars, &tfvars))
			assert.Equal(t, "${AZURE_SUBSCRIPTION_ID}", tfvars["subscription_id"])
			assert.Equal(t, "${AZURE_LOCATION}", tfvars["location"])
			assert.Equal(t, "${AZURE_RESOURCE_GROUP}", tfvars["resource_group_name"])
			assert.Equal(t, "${AZURE_ENV_NAME}", tfvars["environment_name"])
			assert.Equal(t, "${AZURE_AI_PROJECT_NAME}", tfvars["foundry_project_name"])
			assert.Equal(t, "${AZURE_PRINCIPAL_ID}", tfvars["principal_id"])
			assert.Equal(t, "${AZD_RESOURCE_TOKEN_SALT}", tfvars["resource_token_salt"])
			assert.NotContains(t, tfvars, "connectionCredentials")
			assert.NotContains(t, tfvars, "includeAcr")

			connections, ok := tfvars["connections"].([]any)
			require.True(t, ok)
			require.Len(t, connections, 1)
			connection, ok := connections[0].(map[string]any)
			require.True(t, ok)
			credentials, ok := connection["credentials"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "${SEARCH_API_KEY}", credentials["key"])
		})
	}
}

func TestProjectServiceNameDeterministic(t *testing.T) {
	services := map[string]*azdext.ServiceConfig{
		"chat-app":     {Host: "azure.ai.agent"},
		"ai-project":   {Host: "custom"},
		"ai-project-2": {Host: "custom"},
	}
	assert.Equal(t, "new-project", projectServiceName("New Project", services))
	assert.Equal(t, "ai-project-3", projectServiceName("", services))
}

func TestLegacyProjectServiceBodyPreservesConfiguration(t *testing.T) {
	body, err := legacyProjectServiceBody(map[string]any{
		"host":        "azure.ai.agents",
		"endpoint":    "https://old.services.ai.azure.com/api/projects/old",
		"deployments": []any{map[string]any{"name": "chat"}},
		"hooks":       map[string]any{"predeploy": "echo ok"},
		"uses":        []any{"connection"},
		"customField": "preserve-me",
	}, "https://new.services.ai.azure.com/api/projects/new")
	require.NoError(t, err)

	assert.NotContains(t, body, "host")
	assert.Equal(t, "https://new.services.ai.azure.com/api/projects/new", body["endpoint"])
	assert.Contains(t, body, "deployments")
	assert.Contains(t, body, "hooks")
	assert.Contains(t, body, "uses")
	assert.Equal(t, "preserve-me", body["customField"])
}

func TestLegacyProjectServiceBodyRemovesEndpointForNewProject(t *testing.T) {
	body, err := legacyProjectServiceBody(map[string]any{
		"host":     "azure.ai.agents",
		"endpoint": "https://old.services.ai.azure.com/api/projects/old",
		"hooks":    map[string]any{"predeploy": "echo ok"},
	}, "")
	require.NoError(t, err)

	assert.NotContains(t, body, "host")
	assert.NotContains(t, body, "endpoint")
	assert.Contains(t, body, "hooks")
}

func TestDeploymentLocationsExplicitSelectionWins(t *testing.T) {
	locations, err := deploymentLocations(
		[]string{"eastus", "westus"},
		"eastus",
		"westus",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"westus"}, locations)

	_, err = deploymentLocations(
		[]string{"eastus"},
		"eastus",
		"westus",
	)
	require.Error(t, err)
}

func TestDeploymentLocationsUsesProjectLocationByDefault(t *testing.T) {
	locations, err := deploymentLocations(
		[]string{"eastus", "westus"},
		"westus",
		"",
	)
	require.NoError(t, err)
	assert.Equal(t, []string{"westus"}, locations)
}

func TestRequiresExistingProjectID(t *testing.T) {
	existingEndpointService := &projectServiceInfo{
		Resolved: map[string]any{
			"endpoint": "https://account.services.ai.azure.com/api/projects/p",
		},
	}
	tests := []struct {
		name    string
		values  map[string]string
		service *projectServiceInfo
		want    bool
	}{
		{
			name:   "greenfield",
			values: map[string]string{"USE_EXISTING_AI_PROJECT": "false"},
		},
		{
			name:   "existing endpoint marker",
			values: map[string]string{"USE_EXISTING_AI_PROJECT": "true"},
			want:   true,
		},
		{
			name:    "existing endpoint service",
			service: existingEndpointService,
			want:    true,
		},
		{
			name:   "existing project ID",
			values: map[string]string{"AZURE_AI_PROJECT_ID": "project-id"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(
				t,
				test.want,
				requiresExistingProjectID(test.values, test.service),
			)
		})
	}
}

func TestValidateConfiguredProjectIdentity(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/project"
	const projectID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	service := &projectServiceInfo{
		Resolved: map[string]any{"endpoint": endpoint},
	}

	require.NoError(t, validateConfiguredProjectIdentity(
		map[string]string{"AZURE_AI_PROJECT_ID": projectID},
		service,
	))

	err := validateConfiguredProjectIdentity(
		map[string]string{
			"AZURE_AI_PROJECT_ID": strings.Replace(
				projectID, "/projects/project", "/projects/other", 1,
			),
		},
		service,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "identify different projects")

	err = validateConfiguredProjectIdentity(
		map[string]string{"AZURE_AI_PROJECT_ID": "project-id"},
		service,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be a Microsoft.CognitiveServices project")
}

func TestValidateDeploymentSelectionRejectsNegativeCapacity(t *testing.T) {
	require.NoError(t, validateDeploymentSelection(
		deploymentSelectionOptions{Capacity: 0},
	))
	require.NoError(t, validateDeploymentSelection(
		deploymentSelectionOptions{Capacity: 10},
	))

	err := validateDeploymentSelection(
		deploymentSelectionOptions{Capacity: -1},
	)
	require.Error(t, err)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, "invalid_parameter", localErr.Code)
}

func TestValidateAllowedProjectLocationUsesFallback(t *testing.T) {
	require.Error(t, validateAllowedProjectLocation(
		&resolvedProject{},
		[]string{"eastus"},
		"westus",
	))
	require.NoError(t, validateAllowedProjectLocation(
		&resolvedProject{},
		[]string{"eastus"},
		"eastus",
	))
	require.NoError(t, validateAllowedProjectLocation(
		&resolvedProject{Location: "eastus"},
		[]string{"eastus"},
		"westus",
	))
	require.Error(t, validateAllowedProjectLocation(
		&resolvedProject{},
		[]string{"eastus"},
		"",
	))
	require.NoError(t, validateAllowedProjectLocation(
		&resolvedProject{},
		nil,
		"",
	))
}

func TestFillEmptyAzureScope(t *testing.T) {
	t.Run("preserves deployment location", func(t *testing.T) {
		target := &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				Location: "deployment-location",
			},
		}
		fallback := &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				Location: "fallback-location",
			},
		}

		fillEmptyAzureScope(target, fallback)

		assert.Empty(t, target.Scope.SubscriptionId)
		assert.Equal(t, "deployment-location", target.Scope.Location)
	})
	t.Run("fills missing values", func(t *testing.T) {
		target := &azdext.AzureContext{Scope: &azdext.AzureScope{}}
		fallback := &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				TenantId:       "tenant",
				SubscriptionId: "subscription",
				Location:       "location",
				ResourceGroup:  "resource-group",
			},
		}

		fillEmptyAzureScope(target, fallback)

		assert.Equal(t, "tenant", target.Scope.TenantId)
		assert.Equal(t, "subscription", target.Scope.SubscriptionId)
		assert.Equal(t, "location", target.Scope.Location)
		assert.Equal(t, "resource-group", target.Scope.ResourceGroup)
	})
	t.Run("handles missing fallback scope", func(t *testing.T) {
		target := &azdext.AzureContext{Scope: &azdext.AzureScope{}}

		fillEmptyAzureScope(target, &azdext.AzureContext{})

		assert.Empty(t, target.Scope.TenantId)
		assert.Empty(t, target.Scope.SubscriptionId)
		assert.Empty(t, target.Scope.Location)
	})
}

func TestResolveDeploymentAzureContextRequiresSubscription(t *testing.T) {
	_, err := resolveDeploymentAzureContext(
		t.Context(),
		nil,
		map[string]string{"AZURE_AI_DEPLOYMENTS_LOCATION": "eastus"},
		true,
	)
	require.Error(t, err)

	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeMissingAzureSubscription, localErr.Code)
}

func TestDeploymentNoMatchErrorsAreRecoverable(t *testing.T) {
	detail := &errdetails.ErrorInfo{
		Domain: azdext.AiErrorDomain,
		Reason: azdext.AiErrorReasonNoDeploymentMatch,
	}
	st, detailErr := status.New(
		codes.FailedPrecondition,
		"no match",
	).WithDetails(detail)
	require.NoError(t, detailErr)
	err := st.Err()
	assert.True(t, isDeploymentNoMatchError(err))
	assert.True(t, isDeploymentNoMatchError(
		fmt.Errorf("resolve: %w", err),
	))
	assert.False(t, isDeploymentNoMatchError(status.Error(
		codes.PermissionDenied,
		"permission denied",
	)))
}

func TestProjectServiceReferenceMutationPreflight(t *testing.T) {
	service := &projectServiceInfo{
		Name: "foundry",
		Raw: map[string]any{
			"endpoint": "https://account.services.ai.azure.com/api/projects/old",
		},
		Resolved: map[string]any{
			"endpoint": "https://account.services.ai.azure.com/api/projects/old",
		},
		ServiceRef: "./services/foundry.yaml",
	}

	require.NoError(t, validateProjectServiceMutation(
		service,
		"https://account.services.ai.azure.com/api/projects/old",
		"",
	))
	require.Error(t, validateProjectServiceMutation(
		service,
		"https://account.services.ai.azure.com/api/projects/new",
		"",
	))
	require.Error(t, validateProjectServiceMutation(
		service,
		"https://account.services.ai.azure.com/api/projects/old",
		"bicep",
	))

	service.Legacy = true
	require.Error(t, validateProjectServiceMutation(
		service,
		"https://account.services.ai.azure.com/api/projects/old",
		"",
	))
}

func TestProjectEnvironmentTransitions(t *testing.T) {
	old := map[string]string{
		"AZURE_AI_PROJECT_ID":            "old-id",
		"AZURE_AI_ACCOUNT_NAME":          "old-account",
		"AZURE_AI_PROJECT_NAME":          "old-project",
		"FOUNDRY_PROJECT_ENDPOINT":       "https://old.services.ai.azure.com/api/projects/old",
		"AZURE_OPENAI_ENDPOINT":          "https://old.openai.azure.com/",
		"AZURE_RESOURCE_GROUP":           "old-rg",
		"AZURE_AI_DEPLOYMENTS_LOCATION":  "eastus",
		"AZURE_AI_MODEL_DEPLOYMENT_NAME": "chat",
	}
	plan := planProjectEnvironment(old, projectModeExistingEndpoint, &resolvedProject{
		Endpoint:    "https://new.services.ai.azure.com/api/projects/new",
		ProjectName: "new",
	}, true)
	assert.Equal(t, "true", plan.Sets["USE_EXISTING_AI_PROJECT"])
	assert.Equal(t, []string{
		"AZURE_AI_ACCOUNT_NAME",
		"AZURE_AI_DEPLOYMENTS_LOCATION",
		"AZURE_AI_MODEL_DEPLOYMENT_NAME",
		"AZURE_AI_PROJECT_ID",
		"AZURE_OPENAI_ENDPOINT",
		"AZURE_RESOURCE_GROUP",
	}, plan.Unsets)
}

func TestProjectEnvironmentClearsOnlyNonEmptyValues(t *testing.T) {
	old := map[string]string{
		"AZURE_AI_PROJECT_ID":   "old-id",
		"AZURE_RESOURCE_GROUP":  "",
		"AZURE_OPENAI_ENDPOINT": "https://old.openai.azure.com/",
	}
	plan := planProjectEnvironment(old, projectModeExistingEndpoint, &resolvedProject{
		Endpoint: "https://new.services.ai.azure.com/api/projects/new",
	}, false)

	assert.Contains(t, plan.Unsets, "AZURE_AI_PROJECT_ID")
	assert.Contains(t, plan.Unsets, "AZURE_OPENAI_ENDPOINT")
	assert.NotContains(t, plan.Unsets, "AZURE_RESOURCE_GROUP")
}

func TestExistingEndpointModeRejectsManagedDeployments(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/p"
	service := &projectServiceInfo{
		Raw: map[string]any{
			"endpoint":    endpoint,
			"deployments": []any{map[string]any{"name": "chat"}},
		},
		Resolved: map[string]any{
			"endpoint":    endpoint,
			"deployments": []any{map[string]any{"name": "chat"}},
		},
	}

	err := validateExistingEndpointMode(service, endpoint, "", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot retain managed model deployments")
}

func TestExistingEndpointModeRejectsInfrastructureWithoutProjectID(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/p"

	err := validateExistingEndpointMode(nil, endpoint, "bicep", nil, nil)
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(
		t,
		exterrors.CodeInfraEjectRequiresProjectID,
		localErr.Code,
	)
	assert.Contains(t, localErr.Suggestion, "--project-id <resource-id> --infra`")
}

func TestExistingEndpointModeAllowsNetworkOnlyService(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/p"
	service := &projectServiceInfo{
		Raw: map[string]any{
			"endpoint": endpoint,
			"network":  map[string]any{"mode": "managed"},
		},
		Resolved: map[string]any{
			"endpoint": endpoint,
			"network":  map[string]any{"mode": "managed"},
		},
	}

	require.NoError(t, validateExistingEndpointMode(service, endpoint, "", nil, nil))
}

func TestProjectEjectIdentityRejectsEndpointMismatch(t *testing.T) {
	resourceID := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/project"
	err := func() error {
		_, err := projectEjectIdentity(
			"https://other.services.ai.azure.com/api/projects/project",
			resourceID,
			nil,
		)
		return err
	}()
	require.Error(t, err)

	localErr, ok := errors.AsType[*azdext.LocalError](err)
	require.True(t, ok)
	assert.Equal(t, exterrors.CodeInvalidParameter, localErr.Code)
}

func TestExistingEndpointModeRejectsConnections(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/p"
	project := &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"search": {Host: "azure.ai.connection"},
		},
	}

	err := validateExistingEndpointMode(nil, endpoint, "", project, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project connections")
}

func TestExistingEndpointModeRejectsPendingAcr(t *testing.T) {
	const endpoint = "https://account.services.ai.azure.com/api/projects/p"
	values := map[string]string{
		"AI_AGENT_PENDING_PROVISION": "model_deployment,acr",
	}

	err := validateExistingEndpointMode(nil, endpoint, "", nil, values)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pending container registry")
	require.NoError(t, validateExistingEndpointMode(
		nil,
		endpoint,
		"",
		nil,
		map[string]string{
			"AI_AGENT_PENDING_PROVISION":        "acr",
			"AZURE_CONTAINER_REGISTRY_ENDPOINT": "registry.azurecr.io",
		},
	))
}

func TestValidateFoundryProviderRejectsRootProviderWithLayers(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`infra:
  provider: microsoft.foundry
  layers:
    - name: app
      provider: bicep
`),
		0600,
	))

	err := validateFoundryProvider(&azdext.ProjectConfig{
		Path:  root,
		Infra: &azdext.InfraOptions{Provider: "microsoft.foundry"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined with named layers")
}

func TestValidateFoundryProviderAllowsSingleFoundryLayer(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`infra:
  layers:
    - name: foundry
      provider: microsoft.foundry
`),
		0600,
	))

	require.NoError(t, validateFoundryProvider(&azdext.ProjectConfig{
		Path:  root,
		Infra: &azdext.InfraOptions{},
	}))
}

func TestValidateFoundryProviderAllowsDefaultInfraPaths(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "dot slash", path: filepath.FromSlash("./infra")},
		{name: "plain", path: filepath.FromSlash("infra")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(root, "azure.yaml"),
				[]byte("name: test\n"),
				0600,
			))
			require.NoError(t, validateFoundryProvider(&azdext.ProjectConfig{
				Path:  root,
				Infra: &azdext.InfraOptions{Path: tt.path},
			}))
		})
	}
}

func TestValidateFoundryProviderAllowsEjectedTerraformLayer(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`infra:
  layers:
    - name: app
      path: infra/app
      provider: bicep
    - name: foundry
      path: infra/foundry
      provider: terraform
`),
		0600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "infra", "foundry"), 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "infra", "foundry", foundryTerraformMarker),
		[]byte(foundryTerraformMarkerVersion),
		0600,
	))

	require.NoError(t, validateFoundryProvider(&azdext.ProjectConfig{
		Path:  root,
		Infra: &azdext.InfraOptions{},
	}))
}

func TestEjectBicepClearsCustomInfraPath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`name: test
services:
  project:
    host: azure.ai.project
    deployments:
      - name: chat
        model: {format: OpenAI, name: gpt-4.1, version: "2025-04-14"}
        sku: {name: GlobalStandard, capacity: 10}
`),
		0600,
	))

	projectServer := &recordingProjectConfigServer{
		project: &azdext.ProjectConfig{
			Path: root,
			Infra: &azdext.InfraOptions{
				Provider: "microsoft.foundry",
				Path:     "custom-infra",
			},
		},
	}
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	require.NoError(t, ejectProjectInfra(
		t.Context(), client, root, "project", "bicep",
	))
	_, err = os.Stat(filepath.Join(root, "infra", "main.bicep"))
	require.NoError(t, err)
	assert.Contains(t, projectServer.unsetPaths, "infra.path")
}

func TestEjectBicepUsesFoundryLayerPathAndModule(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`name: test
infra:
  layers:
    - name: app
      path: infra/app
      provider: bicep
    - name: foundry
      path: infra/foundry
      module: project
      provider: microsoft.foundry
services:
  project:
    host: azure.ai.project
    deployments:
      - name: chat
        model: {format: OpenAI, name: gpt-4.1, version: "2025-04-14"}
        sku: {name: GlobalStandard, capacity: 10}
`),
		0600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "infra", "app"), 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "infra", "app", "main.bicep"),
		[]byte("targetScope = 'subscription'\n"),
		0600,
	))

	projectServer := &recordingProjectConfigServer{
		project: &azdext.ProjectConfig{
			Path:  root,
			Infra: &azdext.InfraOptions{Provider: "bicep"},
		},
	}
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	require.NoError(t, ejectProjectInfra(
		t.Context(), client, root, "project", "bicep",
	))
	_, err = os.Stat(filepath.Join(root, "infra", "foundry", "project.bicep"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "infra", "foundry", "project.parameters.json"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "infra", "main.bicep"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.NotContains(t, projectServer.unsetPaths, "infra.path")
}

func TestEjectTerraformUsesFoundryLayerPathAndProvider(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`name: test
infra:
  layers:
    - name: app
      path: infra/app
      provider: bicep
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
services:
  project:
    host: azure.ai.project
    deployments:
      - name: chat
        model: {format: OpenAI, name: gpt-4.1, version: "2025-04-14"}
        sku: {name: GlobalStandard, capacity: 10}
`),
		0600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "infra", "app"), 0750))

	projectServer := &recordingProjectConfigServer{
		project: &azdext.ProjectConfig{
			Path:  root,
			Infra: &azdext.InfraOptions{Provider: "bicep"},
		},
	}
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	require.NoError(t, ejectProjectInfra(
		t.Context(), client, root, "project", "terraform",
	))
	_, err = os.Stat(filepath.Join(root, "infra", "foundry", "main.tf"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "infra", "foundry", "main.tfvars.json"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, "infra", "main.tf"))
	assert.ErrorIs(t, err, os.ErrNotExist)
	require.NotNil(t, projectServer.setRequest)
	assert.Equal(t, "infra.layers", projectServer.setRequest.Path)
	layers, ok := projectServer.setRequest.Value.AsInterface().([]any)
	require.True(t, ok)
	require.Len(t, layers, 2)
	foundry, ok := layers[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "terraform", foundry["provider"])
}

func TestLoadDelegatedProjectAddNormalizesInfraProvider(t *testing.T) {
	requestPath := filepath.Join(t.TempDir(), "request.json")
	require.NoError(t, os.WriteFile(
		requestPath,
		[]byte(`{
  "schemaVersion": 1,
  "source": "azure.ai.agents/init",
  "sourceVersion": "1.0.0",
  "project": {"resourceId": "/subscriptions/sub"},
  "infra": {"ejectProvider": " TERRAFORM "}
}`),
		0600,
	))

	action := &ProjectAddAction{
		flags: &projectAddFlags{requestFile: requestPath},
	}
	request, err := action.loadRequest()
	require.NoError(t, err)
	require.NotNil(t, request)
	assert.Equal(t, "terraform", request.Infra.EjectProvider)
	assert.Equal(t, "terraform", action.flags.infra)
}

func TestExpandProjectServiceValuesUsesEnvironment(t *testing.T) {
	raw := map[string]any{
		"endpoint": "${FOUNDRY_PROJECT_ENDPOINT}",
		"nested":   []any{map[string]any{"value": "${PROJECT_VALUE}"}},
	}
	expandedValue, err := expandProjectServiceValues(
		raw,
		map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/p",
			"PROJECT_VALUE":            "expanded",
		},
	)
	require.NoError(t, err)
	expanded, ok := expandedValue.(map[string]any)
	require.True(t, ok)
	assert.Equal(
		t,
		"https://account.services.ai.azure.com/api/projects/p",
		expanded["endpoint"],
	)
	assert.Equal(t, "${FOUNDRY_PROJECT_ENDPOINT}", raw["endpoint"])
}

func TestDiscoverProjectServiceExpandsEndpointFromRef(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "services"), 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "services", "project.yaml"),
		[]byte("endpoint: ${FOUNDRY_PROJECT_ENDPOINT}\n"),
		0600,
	))
	section, err := structpb.NewStruct(map[string]any{
		"project": map[string]any{"$ref": "./services/project.yaml"},
	})
	require.NoError(t, err)

	projectServer := &recordingProjectConfigServer{
		project: &azdext.ProjectConfig{
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"project": {Host: aiProjectHost},
			},
		},
		section: section,
	}
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	reconciler := &projectServiceReconciler{
		client:      client,
		projectRoot: root,
		environmentValues: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT": "https://account.services.ai.azure.com/api/projects/p",
		},
	}
	service, _, err := reconciler.discoverProjectService(t.Context())
	require.NoError(t, err)
	require.NotNil(t, service)
	assert.Equal(
		t,
		"https://account.services.ai.azure.com/api/projects/p",
		service.Resolved["endpoint"],
	)
	assert.Equal(t, "./services/project.yaml", service.ServiceRef)
	assert.NotContains(t, service.Raw, "endpoint")
}

func TestChooseDeploymentNameTrimsExplicitName(t *testing.T) {
	assert.Equal(t, "chat", chooseDeploymentName("  chat  ", "gpt-4.1"))
}

func TestProjectServiceEndpointUsesExactKeyTombstone(t *testing.T) {
	server := grpc.NewServer()
	projectServer := &recordingProjectServiceServer{}
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	require.NoError(t, setProjectServiceEndpoint(
		t.Context(), client, "project", "",
	))
	require.NotNil(t, projectServer.request)
	assert.Equal(t, "project", projectServer.request.ServiceName)
	assert.Equal(t, "endpoint", projectServer.request.Path)
	assert.Equal(t, "", projectServer.request.Value.GetStringValue())
}

func TestAddServicePersistsCompleteBodyThroughConfigSection(t *testing.T) {
	server := grpc.NewServer()
	projectServer := &recordingProjectServiceServer{}
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		client.Close()
	})

	reconciler := &projectServiceReconciler{client: client}
	require.NoError(t, reconciler.addService(
		t.Context(),
		"foundry",
		map[string]any{
			"endpoint":    "https://account.services.ai.azure.com/api/projects/p",
			"deployments": []any{map[string]any{"name": "chat"}},
			"hooks": map[string]any{
				"predeploy": map[string]any{"kind": "sh", "run": "echo ok"},
			},
			"uses":        []any{"connection"},
			"env":         map[string]any{"PROJECT_MODE": "managed"},
			"customField": "preserve-me",
		},
	))

	require.NotNil(t, projectServer.addRequest)
	assert.Nil(t, projectServer.addRequest.Service.AdditionalProperties)
	require.NotNil(t, projectServer.sectionRequest)
	assert.Equal(t, "foundry", projectServer.sectionRequest.ServiceName)
	assert.Empty(t, projectServer.sectionRequest.Path)
	section := projectServer.sectionRequest.Section.AsMap()
	assert.Equal(t, "azure.ai.project", section["host"])
	assert.Contains(t, section, "deployments")
	assert.Contains(t, section, "hooks")
	assert.Contains(t, section, "uses")
	assert.Contains(t, section, "env")
	assert.Equal(t, "preserve-me", section["customField"])
}

func TestAddServiceRollsBackHostOnlyService(t *testing.T) {
	server := grpc.NewServer()
	projectServer := &recordingProjectServiceServer{
		sectionErr: errors.New("configuration write failed"),
	}
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	reconciler := &projectServiceReconciler{client: client}
	require.Error(t, reconciler.addService(
		t.Context(),
		"foundry",
		map[string]any{
			"endpoint": "https://account.services.ai.azure.com/api/projects/p",
		},
	))

	assert.Equal(t, []string{"services.foundry"}, projectServer.unsetPaths)
}

type recordingProjectServiceServer struct {
	azdext.UnimplementedProjectServiceServer
	request        *azdext.SetServiceConfigValueRequest
	addRequest     *azdext.AddServiceRequest
	sectionRequest *azdext.SetServiceConfigSectionRequest
	sectionErr     error
	unsetErr       error
	unsetPaths     []string
}

type recordingProjectConfigServer struct {
	azdext.UnimplementedProjectServiceServer
	project    *azdext.ProjectConfig
	section    *structpb.Struct
	unsetPaths []string
	setRequest *azdext.SetProjectConfigValueRequest
}

type projectAddEnvironmentServer struct {
	azdext.UnimplementedEnvironmentServiceServer
	setCalls int
	setErr   error
	values   map[string]string
}

func (s *projectAddEnvironmentServer) Select(
	context.Context,
	*azdext.SelectEnvironmentRequest,
) (*azdext.EmptyResponse, error) {
	return &azdext.EmptyResponse{}, nil
}

func (s *projectAddEnvironmentServer) GetValues(
	context.Context,
	*azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	response := &azdext.KeyValueListResponse{}
	for key, value := range s.values {
		response.KeyValues = append(response.KeyValues, &azdext.KeyValue{
			Key:   key,
			Value: value,
		})
	}
	return response, nil
}

func (s *projectAddEnvironmentServer) SetValue(
	context.Context,
	*azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.setCalls++
	if s.setErr != nil {
		return nil, s.setErr
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectConfigServer) Get(
	context.Context,
	*azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

func (s *recordingProjectConfigServer) GetConfigSection(
	_ context.Context,
	_ *azdext.GetProjectConfigSectionRequest,
) (*azdext.GetProjectConfigSectionResponse, error) {
	return &azdext.GetProjectConfigSectionResponse{
		Section: s.section,
		Found:   s.section != nil,
	}, nil
}

func (s *recordingProjectConfigServer) UnsetConfig(
	_ context.Context,
	request *azdext.UnsetProjectConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.unsetPaths = append(s.unsetPaths, request.Path)
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectConfigServer) SetConfigValue(
	_ context.Context,
	request *azdext.SetProjectConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.setRequest = request
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServiceServer) AddService(
	_ context.Context,
	request *azdext.AddServiceRequest,
) (*azdext.EmptyResponse, error) {
	s.addRequest = request
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServiceServer) SetServiceConfigSection(
	_ context.Context,
	request *azdext.SetServiceConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	s.sectionRequest = request
	if s.sectionErr != nil {
		return nil, s.sectionErr
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServiceServer) UnsetConfig(
	_ context.Context,
	request *azdext.UnsetProjectConfigRequest,
) (*azdext.EmptyResponse, error) {
	s.unsetPaths = append(s.unsetPaths, request.Path)
	if s.unsetErr != nil {
		return nil, s.unsetErr
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServiceServer) SetServiceConfigValue(
	_ context.Context,
	request *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.request = request
	return &azdext.EmptyResponse{}, nil
}

func TestProjectAddPersistsProjectBeforeEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte("name: test\n"),
		0600,
	))

	projectServer := &recordingProjectConfigServer{
		project: &azdext.ProjectConfig{
			Name:     "test",
			Path:     root,
			Services: map[string]*azdext.ServiceConfig{},
		},
	}
	envServer := &projectAddEnvironmentServer{}
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, projectServer)
	azdext.RegisterEnvironmentServiceServer(server, envServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	action := &ProjectAddAction{
		client: client,
		flags: &projectAddFlags{
			projectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
			noPrompt:        true,
			output:          "none",
		},
		extCtx: &azdext.ExtensionContext{Environment: "test"},
	}
	err = action.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "add project service")
	assert.Zero(t, envServer.setCalls)
}

func TestProjectAddWritesEnvironmentBeforeInfrastructureEjection(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "azure.yaml"),
		[]byte(`name: test
services:
  foundry:
    host: azure.ai.project
`),
		0600,
	))
	section, err := structpb.NewStruct(map[string]any{
		"foundry": map[string]any{"host": aiProjectHost},
	})
	require.NoError(t, err)

	projectServer := &recordingProjectConfigServer{
		project: &azdext.ProjectConfig{
			Name: "test",
			Path: root,
			Services: map[string]*azdext.ServiceConfig{
				"foundry": {Name: "foundry", Host: aiProjectHost},
			},
			Infra: &azdext.InfraOptions{Provider: "microsoft.foundry"},
		},
		section: section,
	}
	envServer := &projectAddEnvironmentServer{
		setErr: errors.New("environment write failed"),
		values: map[string]string{
			"AZURE_SUBSCRIPTION_ID": "subscription",
			"AZURE_LOCATION":        "eastus",
		},
	}
	server := grpc.NewServer()
	azdext.RegisterProjectServiceServer(server, projectServer)
	azdext.RegisterEnvironmentServiceServer(server, envServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(
		azdext.WithAddress(listener.Addr().String()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	action := &ProjectAddAction{
		client: client,
		flags: &projectAddFlags{
			infra:    "terraform",
			noPrompt: true,
			output:   "none",
		},
		extCtx: &azdext.ExtensionContext{Environment: "test"},
	}
	require.Error(t, action.Run(t.Context()))
	assert.Equal(t, 2, envServer.setCalls)
	assert.Nil(t, projectServer.setRequest)
	_, err = os.Stat(filepath.Join(root, "infra"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestProjectEnvironmentPreservesLocationWhenProjectLocationIsUnknown(t *testing.T) {
	old := map[string]string{
		"AZURE_LOCATION":                "westus2",
		"AZURE_AI_DEPLOYMENTS_LOCATION": "eastus",
		"AZURE_AI_PROJECT_ID":           "old-id",
		"FOUNDRY_PROJECT_ENDPOINT":      "https://old.services.ai.azure.com/api/projects/old",
	}
	plan := planProjectEnvironment(old, projectModeExistingID, &resolvedProject{
		ResourceId: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/account/projects/new",
		Endpoint: "https://account.services.ai.azure.com/api/projects/new",
	}, true)

	assert.NotContains(t, plan.Sets, "AZURE_LOCATION")
	assert.NotContains(t, plan.Unsets, "AZURE_LOCATION")
}

func TestProjectEnvironmentPreservesExistingLocation(t *testing.T) {
	plan := planProjectEnvironment(
		map[string]string{"AZURE_LOCATION": "westus2"},
		projectModeExistingID,
		&resolvedProject{Location: "eastus"},
		false,
	)

	assert.NotContains(t, plan.Sets, "AZURE_LOCATION")
	assert.NotContains(t, plan.Unsets, "AZURE_LOCATION")
}

func TestDeploymentSemanticEqualityIgnoresNameCase(t *testing.T) {
	value := map[string]any{
		"name": "Chat",
		"model": map[string]any{
			"format": "OpenAI", "name": "gpt-4.1", "version": "2025-04-14",
		},
		"sku": map[string]any{"name": "GlobalStandard", "capacity": float64(10)},
	}
	assert.True(t, deploymentSemanticallyEqual(value, synthesisDeploymentForTest()))
}

func TestDeploymentItemsUsesExpandedValues(t *testing.T) {
	raw := map[string]any{
		"name": "${DEPLOYMENT_NAME}",
		"model": map[string]any{
			"format": "OpenAI",
			"name":   "${MODEL_NAME}",
		},
		"sku": map[string]any{"name": "GlobalStandard", "capacity": 10},
	}
	expanded := map[string]any{
		"name": "chat",
		"model": map[string]any{
			"format":  "OpenAI",
			"name":    "gpt-4.1",
			"version": "2025-04-14",
		},
		"sku": map[string]any{"name": "GlobalStandard", "capacity": 10},
	}

	rawItems, resolvedItems, err := deploymentItems(
		&projectServiceInfo{
			Raw:      map[string]any{"deployments": []any{raw}},
			Resolved: map[string]any{"deployments": []any{expanded}},
		},
		"",
	)
	require.NoError(t, err)
	require.Len(t, rawItems, 1)
	require.Len(t, resolvedItems, 1)
	assert.Equal(t, "${MODEL_NAME}", rawItems[0]["model"].(map[string]any)["name"])
	assert.Equal(t, "gpt-4.1", resolvedItems[0]["model"].(map[string]any)["name"])
	assert.True(t, deploymentSemanticallyEqual(
		resolvedItems[0],
		synthesisDeploymentForTest(),
	))
}

func TestDeploymentItemsRejectsSectionReference(t *testing.T) {
	_, _, err := deploymentItems(
		&projectServiceInfo{
			Raw: map[string]any{
				"deployments": map[string]any{
					"$ref": "./deployments.yaml",
				},
			},
			Resolved: map[string]any{},
		},
		"",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployments value must be an array")
}

func synthesisDeploymentForTest() synthesis.Deployment {
	return synthesis.Deployment{
		Name: "chat",
		Model: synthesis.DeploymentModel{
			Format: "OpenAI", Name: "gpt-4.1", Version: "2025-04-14",
		},
		Sku: synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}
}
