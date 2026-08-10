// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

type delegatedWorkflowRecorder struct {
	azdext.UnimplementedWorkflowServiceServer
	azdext.UnimplementedProjectServiceServer
	commands        [][]string
	tempDirs        []string
	requests        []map[string]any
	projectMode     string
	deploymentCount int
	service         *azdext.ServiceConfig
	env             *testEnvironmentServiceServer
	runErr          error
}

func (s *delegatedWorkflowRecorder) Run(
	_ context.Context,
	request *azdext.RunWorkflowRequest,
) (*azdext.EmptyResponse, error) {
	args := request.GetWorkflow().GetSteps()[0].GetCommand().GetArgs()
	s.commands = append(s.commands, args)

	var requestPath string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--request-file="):
			requestPath = strings.TrimPrefix(arg, "--request-file=")
		}
	}
	s.tempDirs = append(s.tempDirs, filepath.Dir(requestPath))
	if s.runErr != nil {
		return nil, s.runErr
	}
	data, err := os.ReadFile(requestPath)
	if err != nil {
		return nil, err
	}
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	s.requests = append(s.requests, envelope)

	if strings.Contains(strings.Join(args, " "), "deployment") {
		s.deploymentCount++
		var requestBody delegatedProjectDeploymentRequest
		if err := json.Unmarshal(data, &requestBody); err != nil {
			return nil, err
		}
		name := requestBody.Model.DeploymentName
		if name == "" {
			name = delegatedModelName(requestBody.Model.Name)
		}
		config := project.ServiceTargetAgentConfig{}
		if s.service != nil && s.service.GetAdditionalProperties() != nil {
			if err := project.UnmarshalStruct(
				s.service.GetAdditionalProperties(), &config,
			); err != nil {
				return nil, err
			}
		}
		config.Deployments = append(config.Deployments, project.Deployment{
			Name: name,
			Model: project.DeploymentModel{
				Name:    requestBody.Model.Name,
				Format:  "OpenAI",
				Version: "2025-04-14",
			},
			Sku: project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
		})
		properties, err := project.MarshalStruct(&config)
		if err != nil {
			return nil, err
		}
		s.service = &azdext.ServiceConfig{
			Name:                 "custom-project",
			Host:                 AiProjectHost,
			AdditionalProperties: properties,
		}
	} else {
		mode := s.projectMode
		if mode == "" {
			mode = "existing-id"
		}
		config := project.ServiceTargetAgentConfig{}
		if mode == "existing-endpoint" {
			config.Endpoint = "https://account.services.ai.azure.com/api/projects/chat"
		}
		properties, err := project.MarshalStruct(&config)
		if err != nil {
			return nil, err
		}
		s.service = &azdext.ServiceConfig{
			Name:                 "custom-project",
			Host:                 AiProjectHost,
			AdditionalProperties: properties,
		}
		if s.env != nil {
			if s.env.values == nil {
				s.env.values = map[string]map[string]string{}
			}
			if s.env.values["dev"] == nil {
				s.env.values["dev"] = map[string]string{}
			}
			if mode == "existing-id" {
				s.env.values["dev"]["AZURE_AI_PROJECT_ID"] =
					"/subscriptions/sub/resourceGroups/rg/providers/" +
						"Microsoft.CognitiveServices/accounts/account/projects/chat"
			}
		}
	}
	return &azdext.EmptyResponse{}, nil
}

func (s *delegatedWorkflowRecorder) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	if s.service == nil {
		return &azdext.GetProjectResponse{
			Project: &azdext.ProjectConfig{},
		}, nil
	}
	return &azdext.GetProjectResponse{
		Project: &azdext.ProjectConfig{
			Services: map[string]*azdext.ServiceConfig{
				s.service.GetName(): s.service,
			},
		},
	}, nil
}

func TestConfigureModelChoiceDelegated_MultipleModels(t *testing.T) {
	envName := "dev"
	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{envName: {}},
	}
	recorder := &delegatedWorkflowRecorder{projectMode: "new", env: envServer}
	client := newTestAzdClient(t, envServer, recorder)
	root := t.TempDir()
	action := &InitAction{
		azdClient:     client,
		projectConfig: &azdext.ProjectConfig{Path: root},
		environment:   &azdext.Environment{Name: envName},
		flags:         &initFlags{env: envName, noPrompt: true},
	}
	manifest := &agent_yaml.AgentManifest{
		Name: "delegated-agent",
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Name: "delegated-agent",
				Kind: agent_yaml.AgentKindHosted,
			},
		},
		Resources: []any{
			agent_yaml.ModelResource{
				Resource: agent_yaml.Resource{Name: "chat", Kind: agent_yaml.ResourceKindModel},
				Id:       "gpt-4.1",
			},
			agent_yaml.ModelResource{
				Resource: agent_yaml.Resource{Name: "embed", Kind: agent_yaml.ResourceKindModel},
				Id:       "text-embedding-3-large",
			},
		},
	}

	updated, err := action.configureModelChoiceDelegated(t.Context(), manifest)
	require.NoError(t, err)
	require.Len(t, recorder.commands, 3)
	require.Equal(t, 2, recorder.deploymentCount)
	require.Equal(t, true, recorder.requests[1]["setAsDefault"])
	require.Equal(t, false, recorder.requests[2]["setAsDefault"])
	require.NotNil(t, updated)
}

func TestDelegatedProjectWorkflowMappingAndCleanup(t *testing.T) {
	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{"dev": {}},
	}

	recorder := &delegatedWorkflowRecorder{env: envServer}
	client := newTestAzdClient(
		t,
		envServer,
		recorder,
	)
	root := t.TempDir()
	action := &InitAction{
		azdClient:     client,
		projectConfig: &azdext.ProjectConfig{Path: root},
		environment:   &azdext.Environment{Name: "dev"},
		flags: &initFlags{
			projectResourceId: "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/account/projects/chat",
			infra: "terraform",
			force: true,
			env:   "dev",
		},
	}

	initResult, err := action.delegateProjectInit(t.Context(), []string{"eastus2"})
	require.NoError(t, err)
	require.Equal(t, "custom-project", initResult.ServiceName)
	require.Equal(t, "custom-project", action.projectServiceName)

	deploymentResult, err := action.delegateProjectDeployment(
		t.Context(), "gpt-4.1", "chat", true, []string{"eastus2"},
	)
	require.NoError(t, err)
	require.Equal(t, "chat", deploymentResult.Deployments[0].Name)
	require.Len(t, recorder.commands, 2)

	for _, args := range recorder.commands {
		assertArgContains(t, args, "--output=none")
		assertArgContains(t, args, "--cwd="+root)
		assertArgContains(t, args, "--environment=dev")
		assertArgPrefix(t, args, "--request-file=")
	}
	require.Equal(t, []string{"ai", "project", "init"}, recorder.commands[0][:3])
	require.Equal(t, []string{"ai", "project", "deployment", "add"}, recorder.commands[1][:4])
	require.Equal(t, float64(1), recorder.requests[0]["schemaVersion"])
	require.Equal(t, "terraform", recorder.requests[0]["infra"].(map[string]any)["ejectProvider"])
	require.Equal(t, true, recorder.requests[0]["force"])
	require.Equal(t, []any{"eastus2"},
		recorder.requests[0]["requirements"].(map[string]any)["allowedLocations"])
	require.Equal(t, "gpt-4.1", recorder.requests[1]["model"].(map[string]any)["name"])
	require.Equal(t, true, recorder.requests[1]["setAsDefault"])
	require.Equal(t, []any{"agentsV2"},
		recorder.requests[1]["model"].(map[string]any)["requiredCapabilities"])
	for _, dir := range recorder.tempDirs {
		_, err := os.Stat(dir)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestDelegatedProjectStateModes(t *testing.T) {
	const resourceID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/projects/chat"
	for _, mode := range []string{"new", "existing-id", "existing-endpoint"} {
		t.Run(mode, func(t *testing.T) {
			envServer := &testEnvironmentServiceServer{
				values: map[string]map[string]string{"dev": {}},
			}
			recorder := &delegatedWorkflowRecorder{
				projectMode: mode,
				env:         envServer,
			}
			client := newTestAzdClient(t, envServer, recorder)
			action := &InitAction{
				azdClient:   client,
				environment: &azdext.Environment{Name: "dev"},
				flags:       &initFlags{env: "dev"},
			}
			state, err := action.delegateProjectInit(t.Context(), nil)
			require.NoError(t, err)
			require.Equal(t, mode, state.Mode)
			if mode == "existing-id" {
				require.Equal(t, resourceID, state.ResourceID)
			}
			if mode == "existing-endpoint" {
				require.NotEmpty(t, state.Endpoint)
			}
		})
	}
}

func TestDelegatedProjectWorkflowFailureAndCancellation(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		recorder := &delegatedWorkflowRecorder{runErr: errors.New("workflow failed")}
		client := newTestAzdClient(
			t, &testEnvironmentServiceServer{}, recorder,
		)
		action := &InitAction{
			azdClient:     client,
			projectConfig: &azdext.ProjectConfig{Path: t.TempDir()},
			environment:   &azdext.Environment{Name: "dev"},
			flags:         &initFlags{env: "dev"},
		}
		err := action.runDelegatedProjectStep(
			t.Context(), strings.Fields(delegatedProjectsInit),
			delegatedProjectInitRequest{SchemaVersion: 1},
		)
		require.ErrorContains(t, err, "workflow failed")
		require.NotEmpty(t, recorder.tempDirs)
		for _, dir := range recorder.tempDirs {
			_, statErr := os.Stat(dir)
			require.ErrorIs(t, statErr, os.ErrNotExist)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		recorder := &delegatedWorkflowRecorder{}
		client := newTestAzdClient(
			t, &testEnvironmentServiceServer{}, recorder,
		)
		action := &InitAction{
			azdClient:     client,
			projectConfig: &azdext.ProjectConfig{Path: t.TempDir()},
			environment:   &azdext.Environment{Name: "dev"},
			flags:         &initFlags{env: "dev"},
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := action.runDelegatedProjectStep(
			ctx, strings.Fields(delegatedProjectsInit),
			delegatedProjectInitRequest{SchemaVersion: 1},
		)
		require.Error(t, err)
		require.Empty(t, recorder.tempDirs)
	})
}

func assertArgContains(t *testing.T, args []string, want string) {
	t.Helper()
	require.Contains(t, args, want)
}

func assertArgPrefix(t *testing.T, args []string, prefix string) {
	t.Helper()
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return
		}
	}
	t.Fatalf("arguments %v do not contain prefix %q", args, prefix)
}

func TestSetServiceUsesOrderedMerge(t *testing.T) {
	t.Parallel()

	server := &recordingProjectServer{
		existing: map[string]*azdext.ServiceConfig{
			"agent": {
				Name: "agent",
				Uses: []string{"hand-authored", "project"},
				Host: AiAgentHost,
			},
		},
	}
	client := newProjectRecorderClient(t, server)

	require.NoError(t, setServiceUses(
		t.Context(), client, "agent", []string{"project", "connection"},
	))

	server.mu.Lock()
	defer server.mu.Unlock()
	require.Equal(t, []string{"hand-authored", "project", "connection"}, server.uses["agent"])
}
