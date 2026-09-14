// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestPlanAgentDeployUnifiedFreshProject(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "main.py"),
		[]byte("print('ready')\n"),
		0o600,
	))
	props, err := structpb.NewStruct(map[string]any{
		"kind":        "hosted",
		"name":        "fresh-agent",
		"description": "Fresh agent",
		"protocols": []any{
			map[string]any{"protocol": "responses", "version": "2.0.0"},
		},
		"codeConfiguration": map[string]any{
			"runtime":    "python_3_13",
			"entryPoint": "main.py",
		},
		"container": map[string]any{
			"resources": map[string]any{"cpu": "0.5", "memory": "1Gi"},
		},
	})
	require.NoError(t, err)
	service := &azdext.ServiceConfig{
		Name:                 "fresh-agent",
		Host:                 "azure.ai.agent",
		RelativePath:         ".",
		AdditionalProperties: props,
		Environment: map[string]string{
			"AZURE_AI_MODEL_DEPLOYMENT_NAME": "gpt-5-mini",
			"API_KEY":                        "do-not-print",
		},
	}

	lookupCalled := false
	plan, err := PlanAgentDeploy(t.Context(), AgentDeployPlanOptions{
		ServiceConfig: service,
		ProjectRoot:   projectRoot,
		Lookup: func(context.Context, string, bool) (*agent_api.AgentObject, error) {
			lookupCalled = true
			return nil, nil
		},
	})
	require.NoError(t, err)
	assert.False(t, lookupCalled)
	assert.Equal(t, "fresh-agent", plan.Agent)
	assert.Equal(t, "fresh-agent", plan.Service)
	assert.Equal(t, "azure.yaml", plan.Source)
	assert.Equal(t, "create", plan.Action)
	assert.Equal(t, "unavailableUntilProvision", plan.RemoteComparison)
	assert.Equal(t, "codePackage", plan.Artifact.Type)
	assert.True(t, plan.Artifact.WouldUpload)
	assert.NotEmpty(t, plan.Artifact.SHA256)

	data, err := jsonMarshalPlan(plan)
	require.NoError(t, err)
	assert.NotContains(t, data, "do-not-print")
	assert.Contains(t, data, "redacted")
	assert.Contains(t, data, "gpt-5-mini")
}

func TestPlanAgentDeployLegacyProjectAgentYaml(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "agent.yaml"),
		[]byte("kind: hosted\nname: legacy-agent\nlanguage: python\n"),
		0o600,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(projectRoot, "main.py"),
		[]byte("print('ready')\n"),
		0o600,
	))

	plan, err := PlanAgentDeploy(t.Context(), AgentDeployPlanOptions{
		ServiceConfig: &azdext.ServiceConfig{
			Name: "legacy-agent", Host: "azure.ai.agent", RelativePath: ".",
		},
		ProjectRoot: projectRoot,
	})
	require.NoError(t, err)
	assert.Equal(t, "legacy-agent", plan.Agent)
	assert.Equal(t, "agent.yaml", plan.Source)
	assert.Equal(t, "unavailableUntilProvision", plan.RemoteComparison)
}

func TestPlanAgentDeployNoConfigurationChanges(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted",
		"name": "existing-agent",
		"protocols": []any{
			map[string]any{"protocol": "responses", "version": "2.0.0"},
		},
		"container": map[string]any{
			"resources": map[string]any{"cpu": "0.5", "memory": "1Gi"},
		},
	})
	require.NoError(t, err)
	service := &azdext.ServiceConfig{
		Name:                 "existing-agent",
		Host:                 "azure.ai.agent",
		RelativePath:         ".",
		Image:                "registry.example.com/team/agent:v1",
		Docker:               &azdext.DockerProjectOptions{ImagePassthrough: true},
		AdditionalProperties: props,
	}
	current := &agent_api.AgentObject{Name: "existing-agent"}
	current.Versions.Latest = agent_api.AgentVersionObject{
		Name:     "existing-agent",
		Version:  "7",
		Metadata: map[string]string{"enableVnextExperience": "true"},
		Definition: agent_api.HostedAgentDefinition{
			AgentDefinition: agent_api.AgentDefinition{Kind: agent_api.AgentKindHosted},
			ProtocolVersions: []agent_api.ProtocolVersionRecord{{
				Protocol: agent_api.AgentProtocolResponses,
				Version:  "2.0.0",
			}},
			CPU:    "0.5",
			Memory: "1Gi",
			ContainerConfiguration: &agent_api.ContainerConfigurationAPI{
				Image: "registry.example.com/team/agent:v1",
			},
		},
	}

	plan, err := PlanAgentDeploy(t.Context(), AgentDeployPlanOptions{
		ServiceConfig:   service,
		ProjectRoot:     projectRoot,
		ProjectEndpoint: "https://account.services.ai.azure.com/api/projects/project",
		Lookup: func(_ context.Context, name string, _ bool) (*agent_api.AgentObject, error) {
			assert.Equal(t, "existing-agent", name)
			return current, nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "createVersion", plan.Action)
	assert.Equal(t, "compared", plan.RemoteComparison)
	assert.Empty(t, plan.Changes)
	assert.False(t, plan.Artifact.WouldBuild)
	assert.False(t, plan.Artifact.WouldPush)
}

func TestRedactDeployPlanImage(t *testing.T) {
	t.Parallel()

	assert.Equal(
		t,
		"https://registry.example.com/team/agent:v1",
		redactDeployPlanImage("https://user:password@registry.example.com/team/agent:v1?sig=secret#fragment"),
	)
	assert.Equal(
		t,
		"registry.example.com/team/agent:v1",
		redactDeployPlanImage("user:password@registry.example.com/team/agent:v1?sig=secret"),
	)
	assert.Equal(
		t,
		"localhost:5000/team/agent@sha256:abc",
		redactDeployPlanImage("localhost:5000/team/agent@sha256:abc"),
	)
}

func jsonMarshalPlan(plan *AgentDeployPlan) (string, error) {
	data, err := json.Marshal(plan)
	return string(data), err
}
