// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDelegatedProjectInitRequestValidation(t *testing.T) {
	request := &projectInitRequest{
		SchemaVersion: delegatedSchemaVersion,
		Source:        projectInitSourceAgents,
		SourceVersion: "1.0.0-beta.9",
		Project:       delegatedProject{ResourceID: "/subscriptions/s"},
	}
	require.NoError(t, request.validate())

	request.Project.Endpoint = "https://account.services.ai.azure.com/api/projects/p"
	require.Error(t, request.validate())
	request.Project.Endpoint = ""
	request.SchemaVersion = 3
	err := request.validate()
	require.Error(t, err)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, azdext.LocalErrorCategoryCompatibility, localErr.Category)
}

func TestDelegatedProjectInitDeploymentReplacement(t *testing.T) {
	request := &projectInitRequest{
		SchemaVersion:      delegatedSchemaVersion,
		Source:             projectInitSourceAgents,
		SourceVersion:      "1.0.0-beta.9",
		ReplaceDeployments: true,
		Deployments: []delegatedDeployment{{
			Name: "chat",
		}},
	}
	require.NoError(t, request.validate())

	request.Deployments = []delegatedDeployment{{Name: "Chat"}, {Name: "chat"}}
	require.Error(t, request.validate())
}

func TestDelegatedRequestRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	require.NoError(t, os.WriteFile(requestPath, []byte(`{
		"schemaVersion": 2,
		"source": "azure.ai.agents/init",
		"sourceVersion": "1.0.0",
		"unknown": true
	}`), 0600))
	request := &projectInitRequest{}
	require.Error(t, decodeDelegatedJSON(requestPath, request))
}

func TestDelegatedRequestIsInputOnly(t *testing.T) {
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "request.json")
	require.NoError(t, os.WriteFile(requestPath, []byte(`{}`), 0600))
	root := NewRootCommand()
	initCommand, _, err := root.Find([]string{"init"})
	require.NoError(t, err)
	deploymentCommand, _, err := root.Find([]string{"deployment", "add"})
	require.NoError(t, err)
	assert.Nil(t, initCommand.Flags().Lookup("result-file"))
	assert.Nil(t, deploymentCommand.Flags().Lookup("result-file"))
	assert.NoError(t, validateDelegatedFilePath(requestPath, "request", true))
}

func TestProjectCommandsRegistered(t *testing.T) {
	root := NewRootCommand()
	initCommand, _, err := root.Find([]string{"init"})
	require.NoError(t, err)
	assert.Equal(t, "init", initCommand.Name())
	deploymentCommand, _, err := root.Find([]string{"deployment", "add"})
	require.NoError(t, err)
	assert.Equal(t, "add", deploymentCommand.Name())
	assert.Equal(t, "bicep", initCommand.Flags().Lookup("infra").NoOptDefVal)
	assert.True(t, initCommand.Flags().Lookup("request-file").Hidden)
	assert.True(t, deploymentCommand.Flags().Lookup("request-file").Hidden)
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
			// #nosec G304
			marker, err := os.ReadFile(
				filepath.Join(infraDir, projectTerraformMarker),
			)
			require.NoError(t, err)
			assert.Equal(t, projectTerraformMarkerV1, string(marker))
			if test.includeAcr {
				assert.Contains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_ENDPOINT")
				_, err := os.Stat(filepath.Join(infraDir, "acr.tf"))
				assert.NoError(t, err)
			} else {
				assert.NotContains(t, string(outputs), "AZURE_CONTAINER_REGISTRY_ENDPOINT")
				_, err := os.Stat(filepath.Join(infraDir, "acr.tf"))
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

func TestProjectInfraEjectUsesLayerModule(t *testing.T) {
	projectRoot := t.TempDir()
	raw := []byte(`infra:
  layers:
    - name: platform
      path: infra/platform
      provider: bicep
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
      module: project
`)

	plan, err := planProjectInfraEject(projectRoot, raw, "bicep")
	require.NoError(t, err)
	assert.Equal(t, "project", plan.module)
	assert.Equal(
		t,
		filepath.Join(projectRoot, "infra", "foundry"),
		plan.targetDir,
	)
}

func TestCopyEmbeddedBicepUsesModuleName(t *testing.T) {
	infraDir := filepath.Join(t.TempDir(), "infra")
	require.NoError(t, os.MkdirAll(infraDir, 0750))

	require.NoError(t, copyEmbeddedBicep(infraDir, "project"))
	assert.FileExists(t, filepath.Join(infraDir, "project.bicep"))
	assert.NoFileExists(t, filepath.Join(infraDir, "main.bicep"))
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

func TestLegacyProjectServiceBodyPreservesProjectConfiguration(t *testing.T) {
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
	assert.NotContains(t, body, "hooks")
	assert.NotContains(t, body, "uses")
	assert.NotContains(t, body, "customField")
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
	assert.NotContains(t, body, "hooks")
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

func TestAddServicePersistsCompleteBodyAtomically(t *testing.T) {
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
	require.NotNil(t, projectServer.addRequest.Service.AdditionalProperties)
	assert.Nil(t, projectServer.sectionRequest)
	section := projectServer.addRequest.Service.AdditionalProperties.AsMap()
	assert.Equal(t, "https://account.services.ai.azure.com/api/projects/p", section["endpoint"])
	assert.Contains(t, section, "deployments")
	assert.Contains(t, section, "hooks")
	assert.Contains(t, section, "uses")
	assert.Contains(t, section, "env")
	assert.Equal(t, "preserve-me", section["customField"])
}

type recordingProjectServiceServer struct {
	azdext.UnimplementedProjectServiceServer
	request        *azdext.SetServiceConfigValueRequest
	addRequest     *azdext.AddServiceRequest
	sectionRequest *azdext.SetServiceConfigSectionRequest
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
	return &azdext.EmptyResponse{}, nil
}

func (s *recordingProjectServiceServer) SetServiceConfigValue(
	_ context.Context,
	request *azdext.SetServiceConfigValueRequest,
) (*azdext.EmptyResponse, error) {
	s.request = request
	return &azdext.EmptyResponse{}, nil
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

func synthesisDeploymentForTest() synthesis.Deployment {
	return synthesis.Deployment{
		Name: "chat",
		Model: synthesis.DeploymentModel{
			Format: "OpenAI", Name: "gpt-4.1", Version: "2025-04-14",
		},
		Sku: synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}
}

func TestPlanProjectInfraEjectMergesExistingFoundryDirectory(t *testing.T) {
	projectRoot := t.TempDir()
	raw := []byte(`name: test
infra:
  provider: bicep
services:
  foundry:
    host: azure.ai.project
`)
	require.NoError(t, os.MkdirAll(
		filepath.Join(projectRoot, "infra", "foundry"), 0750,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "main.bicep"),
		[]byte("// existing infrastructure\n"), 0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "infra", "foundry", "README.md"),
		[]byte("user-owned notes\n"), 0600,
	))

	plan, err := planProjectInfraEject(projectRoot, raw, "bicep")
	require.NoError(t, err)
	assert.True(t, plan.layer)
	assert.True(t, plan.mergeExisting)
	assert.Equal(
		t,
		filepath.Join(projectRoot, "infra", "foundry"),
		plan.targetDir,
	)
	assert.Contains(t, string(plan.updatedYAML), "name: foundry")
}

func TestInstallProjectInfraStageMergesAndRollsBack(t *testing.T) {
	projectRoot := t.TempDir()
	targetDir := filepath.Join(projectRoot, "infra", "foundry")
	stageDir := filepath.Join(projectRoot, ".stage")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	require.NoError(t, os.MkdirAll(
		filepath.Join(targetDir, "modules"), 0750,
	))
	require.NoError(t, os.MkdirAll(
		filepath.Join(stageDir, "modules"), 0750,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "README.md"),
		[]byte("keep me\n"), 0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(stageDir, "main.bicep"),
		[]byte("generated\n"), 0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(stageDir, "modules", "generated.bicep"),
		[]byte("generated module\n"), 0600,
	))

	rollback, err := installProjectInfraStage(stageDir, &projectInfraEjectPlan{
		targetDir:     targetDir,
		targetPath:    "infra/foundry",
		mergeExisting: true,
	})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(targetDir, "README.md"))
	assert.FileExists(t, filepath.Join(targetDir, "main.bicep"))
	assert.FileExists(t, filepath.Join(
		targetDir, "modules", "generated.bicep",
	))

	rollback()
	assert.FileExists(t, filepath.Join(targetDir, "README.md"))
	assert.NoFileExists(t, filepath.Join(targetDir, "main.bicep"))
	assert.DirExists(t, filepath.Join(targetDir, "modules"))
	assert.NoFileExists(t, filepath.Join(
		targetDir, "modules", "generated.bicep",
	))
}

func TestInstallProjectInfraStageRejectsConflictsBeforeWriting(t *testing.T) {
	projectRoot := t.TempDir()
	targetDir := filepath.Join(projectRoot, "infra", "foundry")
	stageDir := filepath.Join(projectRoot, ".stage")
	require.NoError(t, os.MkdirAll(targetDir, 0750))
	require.NoError(t, os.MkdirAll(stageDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(targetDir, "main.bicep"),
		[]byte("original\n"), 0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(stageDir, "main.bicep"),
		[]byte("replacement\n"), 0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(stageDir, "new.bicep"),
		[]byte("new\n"), 0600,
	))

	_, err := installProjectInfraStage(stageDir, &projectInfraEjectPlan{
		targetDir:     targetDir,
		targetPath:    "infra/foundry",
		mergeExisting: true,
	})
	require.Error(t, err)
	// #nosec G304
	content, err := os.ReadFile(filepath.Join(targetDir, "main.bicep"))
	require.NoError(t, err)
	assert.Equal(t, "original\n", string(content))
	assert.NoFileExists(t, filepath.Join(targetDir, "new.bicep"))
}

func TestInstallProjectInfraStageRollbackRestoresEmptyTarget(t *testing.T) {
	projectRoot := t.TempDir()
	targetDir := filepath.Join(projectRoot, "infra", "foundry")
	stageDir := filepath.Join(projectRoot, ".stage")
	require.NoError(t, os.MkdirAll(stageDir, 0750))
	require.NoError(t, os.WriteFile(
		filepath.Join(stageDir, "main.bicep"),
		[]byte("generated\n"), 0600,
	))

	rollback, err := installProjectInfraStage(stageDir, &projectInfraEjectPlan{
		targetDir:  targetDir,
		targetPath: "infra/foundry",
	})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(targetDir, "main.bicep"))

	rollback()
	empty, err := projectInfraDirectoryEmpty(targetDir)
	require.NoError(t, err)
	assert.True(t, empty)
}

func TestProjectInfraTerraformOwnershipDetection(t *testing.T) {
	dir := t.TempDir()
	markerPath := filepath.Join(dir, projectTerraformMarker)
	require.NoError(t, os.WriteFile(
		markerPath, []byte(projectTerraformMarkerV1), 0600,
	))

	owned, err := projectInfraHasEntrypoint(
		dir, provisioning.TerraformProviderName, "main",
	)
	require.NoError(t, err)
	assert.True(t, owned)

	require.NoError(t, os.WriteFile(
		markerPath, []byte("edited\n"), 0600,
	))
	owned, err = projectInfraHasEntrypoint(
		dir, provisioning.TerraformProviderName, "main",
	)
	require.Error(t, err)
	assert.False(t, owned)
	var localErr *azdext.LocalError
	require.ErrorAs(t, err, &localErr)
	assert.Equal(t, exterrors.CodeInfraEjectMarkerInvalid, localErr.Code)

	require.NoError(t, os.Remove(markerPath))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "main.tfvars.json"), []byte("{}\n"), 0600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "main.tf"),
		[]byte("resource \"azurerm_resource_group\" \"app\" {}\n"), 0600,
	))
	owned, err = projectInfraHasEntrypoint(
		dir, provisioning.TerraformProviderName, "main",
	)
	require.NoError(t, err)
	assert.False(t, owned)

	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "main.tf"),
		[]byte(`resource "azapi_resource" "foundry_account" {}
resource "azapi_resource" "project" {}
Microsoft.CognitiveServices/accounts
Microsoft.CognitiveServices/accounts/projects
`), 0600,
	))
	owned, err = projectInfraHasEntrypoint(
		dir, provisioning.TerraformProviderName, "main",
	)
	require.NoError(t, err)
	assert.True(t, owned)
}

func TestPlanProjectInfraEjectRejectsUnsafePaths(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()
	absolute := filepath.ToSlash(outside)
	traversal, err := filepath.Rel(projectRoot, outside)
	require.NoError(t, err)

	for _, path := range []string{".", traversal, absolute} {
		t.Run(path, func(t *testing.T) {
			raw := fmt.Appendf(nil, `name: test
infra:
  layers:
    - name: foundry
      path: %s
      provider: microsoft.foundry
services:
  foundry:
    host: azure.ai.project
`, path)
			_, err := planProjectInfraEject(projectRoot, raw, "bicep")
			require.Error(t, err)
			assert.NoFileExists(t, filepath.Join(outside, "main.bicep"))
		})
	}
}

func TestPlanProjectInfraEjectRejectsSymlinkedTarget(t *testing.T) {
	projectRoot := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "infra"), 0750))
	if err := os.Symlink(
		outside, filepath.Join(projectRoot, "infra", "foundry"),
	); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	raw := []byte(`name: test
infra:
  layers:
    - name: app
      path: infra/app
      provider: bicep
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
services:
  foundry:
    host: azure.ai.project
`)

	_, err := planProjectInfraEject(projectRoot, raw, "bicep")
	require.Error(t, err)
	assert.NoFileExists(t, filepath.Join(outside, "main.bicep"))
}
