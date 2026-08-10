// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

type delegatedWorkflowRecorder struct {
	azdext.UnimplementedWorkflowServiceServer
	commands        [][]string
	tempDirs        []string
	requests        []map[string]any
	projectMode     string
	deploymentCount int
}

func (s *delegatedWorkflowRecorder) Run(
	_ context.Context,
	request *azdext.RunWorkflowRequest,
) (*azdext.EmptyResponse, error) {
	args := request.GetWorkflow().GetSteps()[0].GetCommand().GetArgs()
	s.commands = append(s.commands, args)

	var requestPath, resultPath string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--request-file="):
			requestPath = strings.TrimPrefix(arg, "--request-file=")
		case strings.HasPrefix(arg, "--result-file="):
			resultPath = strings.TrimPrefix(arg, "--result-file=")
		}
	}
	s.tempDirs = append(s.tempDirs, filepath.Dir(requestPath))
	data, err := os.ReadFile(requestPath)
	if err != nil {
		return nil, err
	}
	var envelope map[string]any
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	s.requests = append(s.requests, envelope)

	var result any
	if strings.Contains(strings.Join(args, " "), "deployment") {
		s.deploymentCount++
		result = delegatedProjectDeploymentResult{
			SchemaVersion:   1,
			ProducerVersion: "projects-test",
			ServiceName:     "custom-project",
			DeploymentName:  fmt.Sprintf("deployment-%d", s.deploymentCount),
			Model: delegatedProjectResultModel{
				Format:  "OpenAI",
				Name:    "gpt-4.1",
				Version: "2025-04-14",
			},
			SKU:      delegatedProjectResultSKU{Name: "GlobalStandard", Capacity: 10},
			Mutation: "created",
		}
	} else {
		mode := s.projectMode
		if mode == "" {
			mode = "existing-id"
		}
		result = delegatedProjectInitResult{
			SchemaVersion:   1,
			ProducerVersion: "projects-test",
			ServiceName:     "custom-project",
			Mode:            mode,
			Mutation:        "created",
			Endpoint:        "https://account.services.ai.azure.com/api/projects/chat",
			ResourceID: "/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.CognitiveServices/accounts/account/projects/chat",
		}
		if mode == "new" {
			result = delegatedProjectInitResult{
				SchemaVersion:   1,
				ProducerVersion: "projects-test",
				ServiceName:     "custom-project",
				Mode:            mode,
				Mutation:        "created",
			}
		}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(resultPath, encoded, 0600); err != nil {
		return nil, err
	}
	_ = envelope
	return &azdext.EmptyResponse{}, nil
}

func TestConfigureModelChoiceDelegated_MultipleModels(t *testing.T) {
	envName := "dev"
	envServer := &testEnvironmentServiceServer{
		values: map[string]map[string]string{envName: {}},
	}
	recorder := &delegatedWorkflowRecorder{projectMode: "new"}
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
	recorder := &delegatedWorkflowRecorder{}
	client := newTestAzdClient(
		t,
		&testEnvironmentServiceServer{},
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
	require.Equal(t, "deployment-1", deploymentResult.DeploymentName)
	require.Len(t, recorder.commands, 2)

	for _, args := range recorder.commands {
		assertArgContains(t, args, "--output=none")
		assertArgContains(t, args, "--cwd="+root)
		assertArgContains(t, args, "--environment=dev")
		assertArgPrefix(t, args, "--request-file=")
		assertArgPrefix(t, args, "--result-file=")
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
