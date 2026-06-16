// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// capturingProjectServiceServer records the AddService request so tests can
// assert the service entry init writes into azure.yaml.
type capturingProjectServiceServer struct {
	azdext.UnimplementedProjectServiceServer
	mu      sync.Mutex
	lastReq *azdext.AddServiceRequest
}

func (s *capturingProjectServiceServer) AddService(
	_ context.Context, req *azdext.AddServiceRequest,
) (*azdext.EmptyResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReq = req
	return &azdext.EmptyResponse{}, nil
}

func (s *capturingProjectServiceServer) captured() *azdext.AddServiceRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastReq
}

// newTestAzdClientWithProject builds an azd client backed by a gRPC server that
// registers a project service (to capture AddService) plus permissive env and
// workflow stubs, so init's addToProject can run end to end in a test.
func newTestAzdClientWithProject(
	t *testing.T,
	projectServer azdext.ProjectServiceServer,
	envServer azdext.EnvironmentServiceServer,
) *azdext.AzdClient {
	t.Helper()

	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, projectServer)
	azdext.RegisterEnvironmentServiceServer(grpcServer, envServer)
	azdext.RegisterWorkflowServiceServer(grpcServer, &testWorkflowServiceServer{})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	return client
}

// TestInitActionAddToProjectWritesFoundryService verifies the manifest/template
// init path writes a host: microsoft.foundry service with the agent inline and
// the Foundry keys on AdditionalProperties (not config:), instead of the old
// host: azure.ai.agent + config: shape.
func TestInitActionAddToProjectWritesFoundryService(t *testing.T) {
	projectServer := &capturingProjectServiceServer{}
	client := newTestAzdClientWithProject(t, projectServer, &testEnvironmentServiceServer{})

	desc := "A basic agent"
	manifest := &agent_yaml.AgentManifest{
		Template: agent_yaml.ContainerAgent{
			AgentDefinition: agent_yaml.AgentDefinition{
				Kind:        agent_yaml.AgentKindHosted,
				Name:        "basic-agent",
				Description: &desc,
			},
			Protocols:         []agent_yaml.ProtocolVersionRecord{{Protocol: "responses", Version: "1.0.0"}},
			CodeConfiguration: &agent_yaml.CodeConfiguration{Runtime: "python_3_13", EntryPoint: "main.py"},
			EnvironmentVariables: &[]agent_yaml.EnvironmentVariable{
				{Name: "FOUNDRY_MODEL_DEPLOYMENT_NAME", Value: "gpt-4o-mini"},
			},
		},
	}

	a := &InitAction{
		azdClient:           client,
		environment:         &azdext.Environment{Name: "test"},
		projectConfig:       &azdext.ProjectConfig{Path: t.TempDir()},
		flags:               &initFlags{noPrompt: true},
		serviceNameOverride: "ai",
		isCodeDeploy:        true,
		deploymentDetails: []project.Deployment{{
			Name:  "gpt-4o-mini",
			Model: project.DeploymentModel{Name: "gpt-4o-mini", Format: "OpenAI", Version: "2024-07-18"},
			Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
		}},
		containerSettings: &project.ContainerSettings{
			Resources: &project.ResourceSettings{Cpu: "0.5", Memory: "1Gi"},
		},
	}

	if err := a.addToProject(t.Context(), "src/basic-agent", manifest); err != nil {
		t.Fatalf("addToProject: %v", err)
	}

	req := projectServer.captured()
	require.NotNil(t, req, "AddService was not called")
	sc := req.Service
	require.Equal(t, project.FoundryHost, sc.Host)
	require.Equal(t, "ai", sc.Name)
	require.Nil(t, sc.Config, "Config must be nil; Foundry keys travel on AdditionalProperties")
	require.Empty(t, sc.RelativePath, "service-level project must be empty (project is on the agent)")
	require.NotNil(t, sc.AdditionalProperties)

	var props project.FoundryServiceProperties
	require.NoError(t, project.UnmarshalStruct(sc.AdditionalProperties, &props))
	require.Len(t, props.Deployments, 1)
	require.Equal(t, "gpt-4o-mini", props.Deployments[0].Name)
	require.Len(t, props.Agents, 1)

	ag := props.Agents[0]
	require.Equal(t, "basic-agent", ag.Name)
	require.Equal(t, "hosted", ag.Kind)
	require.Equal(t, "src/basic-agent", ag.Project)
	require.NotNil(t, ag.Runtime)
	require.Equal(t, "python", ag.Runtime.Stack)
	require.Equal(t, "3.13", ag.Runtime.Version)
	require.Equal(t, "python main.py", ag.StartupCommand)
	require.Nil(t, ag.Docker, "code deploy must not set docker")
	require.Equal(t, "gpt-4o-mini", ag.Env["FOUNDRY_MODEL_DEPLOYMENT_NAME"])
	require.NotNil(t, ag.Container)
	require.Equal(t, "0.5", ag.Container.Resources.Cpu)
}

// TestInitFromCodeActionAddToProjectWritesFoundryService verifies the from-code
// init path writes the same unified shape, deriving the runtime + startup command
// from the on-disk agent definition.
func TestInitFromCodeActionAddToProjectWritesFoundryService(t *testing.T) {
	projectServer := &capturingProjectServiceServer{}
	client := newTestAzdClientWithProject(t, projectServer, &testEnvironmentServiceServer{})

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src", "agent")
	require.NoError(t, os.MkdirAll(srcDir, 0750))
	agentYaml := "kind: hosted\nname: my-agent\ncode_configuration:\n  runtime: python_3_12\n  entry_point: main.py\n"
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "agent.yaml"), []byte(agentYaml), 0600))

	a := &InitFromCodeAction{
		azdClient:     client,
		environment:   &azdext.Environment{Name: "test"},
		projectConfig: &azdext.ProjectConfig{Path: tmpDir},
		flags:         &initFlags{noPrompt: true},
	}

	if err := a.addToProject(t.Context(), "src/agent", "my-agent", true); err != nil {
		t.Fatalf("addToProject: %v", err)
	}

	req := projectServer.captured()
	require.NotNil(t, req, "AddService was not called")
	sc := req.Service
	require.Equal(t, project.FoundryHost, sc.Host)
	require.Equal(t, "my-agent", sc.Name)
	require.Nil(t, sc.Config)

	var props project.FoundryServiceProperties
	require.NoError(t, project.UnmarshalStruct(sc.AdditionalProperties, &props))
	require.Len(t, props.Agents, 1)

	ag := props.Agents[0]
	require.Equal(t, "my-agent", ag.Name)
	require.Equal(t, "hosted", ag.Kind)
	require.Equal(t, "src/agent", ag.Project)
	require.NotNil(t, ag.Runtime)
	require.Equal(t, "python", ag.Runtime.Stack)
	require.Equal(t, "3.12", ag.Runtime.Version)
	require.Equal(t, "python main.py", ag.StartupCommand)
}

// TestEnsureAgentIgnore checks the unified flow writes .agentignore (for
// code-deploy packaging) but never agent.yaml, and is idempotent.
func TestEnsureAgentIgnore(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, ensureAgentIgnore(tmpDir))
	_, err := os.Stat(filepath.Join(tmpDir, ".agentignore"))
	require.NoError(t, err, ".agentignore should be written")

	_, err = os.Stat(filepath.Join(tmpDir, "agent.yaml"))
	require.True(t, os.IsNotExist(err), "agent.yaml must not be written by the unified flow")

	// Idempotent: a second call must not error.
	require.NoError(t, ensureAgentIgnore(tmpDir))
}
