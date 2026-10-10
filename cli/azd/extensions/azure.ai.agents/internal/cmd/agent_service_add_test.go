// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

const agentServiceAddDefinition = `kind: prompt
name: support-agent
model: gpt-4.1-mini
instructions: Help the user.
`

type recordingAgentServiceAddClient struct {
	azdext.ProjectServiceClient

	projectPath string
	getErr      error
	addErr      error
	setErr      error
	getCalls    int
	addRequests []*azdext.AddServiceRequest
	setRequests []*azdext.SetServiceConfigSectionRequest
}

func (c *recordingAgentServiceAddClient) Get(
	ctx context.Context,
	_ *azdext.EmptyRequest,
	_ ...grpc.CallOption,
) (*azdext.GetProjectResponse, error) {
	c.getCalls++
	if c.getErr != nil {
		return nil, c.getErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &azdext.GetProjectResponse{
		Project: &azdext.ProjectConfig{Path: c.projectPath},
	}, nil
}

func (c *recordingAgentServiceAddClient) AddService(
	_ context.Context,
	request *azdext.AddServiceRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	c.addRequests = append(c.addRequests, proto.Clone(request).(*azdext.AddServiceRequest))
	if c.addErr != nil {
		return nil, c.addErr
	}
	return &azdext.EmptyResponse{}, nil
}

func (c *recordingAgentServiceAddClient) SetServiceConfigSection(
	_ context.Context,
	request *azdext.SetServiceConfigSectionRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	c.setRequests = append(
		c.setRequests,
		proto.Clone(request).(*azdext.SetServiceConfigSectionRequest),
	)
	if c.setErr != nil {
		return nil, c.setErr
	}
	return &azdext.EmptyResponse{}, nil
}

func executeAgentServiceAddCommand(
	t *testing.T,
	newProjectClient agentServiceAddClientFactory,
	ctx context.Context,
	args ...string,
) (string, string, error) {
	t.Helper()

	extCtx := &azdext.ExtensionContext{}
	root := &cobra.Command{
		Use:           "agent",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.PersistentFlags().StringVar(&extCtx.OutputFormat, "output", "default", "Output format")
	root.AddCommand(newAgentServiceAddCommandWithClientFactory(extCtx, newProjectClient))
	var out bytes.Buffer
	var errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	if ctx != nil {
		root.SetContext(ctx)
	}
	root.SetArgs(append([]string{"add"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func executeAgentServiceAddWithRecordingClient(
	t *testing.T,
	client *recordingAgentServiceAddClient,
	args ...string,
) (string, string, error) {
	t.Helper()
	return executeAgentServiceAddCommand(
		t,
		func() (azdext.ProjectServiceClient, func(), error) {
			return client, nil, nil
		},
		nil,
		args...,
	)
}

func writeAgentServiceAddProject(
	t *testing.T,
	projectRoot string,
	services string,
) {
	t.Helper()
	content := "name: agent-add-test\nservices:\n" + services
	require.NoError(
		t,
		os.WriteFile(filepath.Join(projectRoot, "azure.yaml"), []byte(content), 0o600),
	)
}

func writeAgentServiceAddDefinition(
	t *testing.T,
	projectRoot string,
	relativePath string,
) {
	t.Helper()
	definitionPath := filepath.Join(projectRoot, filepath.FromSlash(relativePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(definitionPath), 0o750))
	require.NoError(
		t,
		os.WriteFile(definitionPath, []byte(agentServiceAddDefinition), 0o600),
	)
}

func agentServiceAddFactory(
	client azdext.ProjectServiceClient,
) agentServiceAddClientFactory {
	return func() (azdext.ProjectServiceClient, func(), error) {
		return client, nil, nil
	}
}

func TestAgentServiceAddCommand_AddsServiceWithOneSDKRequest(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAgentServiceAddProject(t, projectRoot, `  ai-project:
    host: azure.ai.project
    project: .
  app:
    host: containerapp
    project: src/app
    env:
      API_KEY: ${APP_API_KEY}
`)
	writeAgentServiceAddDefinition(t, projectRoot, "definitions/support.yaml")
	require.NoError(t, os.MkdirAll(filepath.Join(projectRoot, "src"), 0o750))

	client := &recordingAgentServiceAddClient{projectPath: projectRoot}
	stdout, stderr, err := executeAgentServiceAddWithRecordingClient(
		t,
		client,
		"support-agent",
		"--file", "./definitions/support.yaml",
		"--project", "ai-project",
		"--source", "./src",
		"--output", "json",
	)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Len(t, client.addRequests, 1)
	require.Empty(t, client.setRequests)

	added := client.addRequests[0].GetService()
	require.Equal(t, "support-agent", added.GetName())
	require.Equal(t, AiAgentHost, added.GetHost())
	require.Equal(t, "src", added.GetRelativePath())
	require.Equal(t, []string{"ai-project"}, added.GetUses())
	require.Equal(
		t,
		"./definitions/support.yaml",
		added.GetAdditionalProperties().GetFields()["$ref"].GetStringValue(),
	)

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, map[string]any{
		"name":         "support-agent",
		"host":         AiAgentHost,
		"mutation":     "added",
		"ref":          "./definitions/support.yaml",
		"project":      "src",
		"dependencies": []any{"ai-project"},
	}, result)
}

func TestAgentServiceAddCommand_AddsJSONDefinitionAsProjectRootRef(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAgentServiceAddProject(t, projectRoot, `  ai-project:
    host: azure.ai.project
`)
	definitionPath := filepath.Join(projectRoot, "definitions", "support.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(definitionPath), 0o750))
	require.NoError(t, os.WriteFile(
		definitionPath,
		[]byte(`{"kind":"prompt","name":"support-agent","model":"gpt-4.1-mini","instructions":"Help the user."}`),
		0o600,
	))

	client := &recordingAgentServiceAddClient{projectPath: projectRoot}
	_, _, err := executeAgentServiceAddWithRecordingClient(
		t,
		client,
		"support-agent",
		"--file", "./definitions/support.json",
		"--output", "json",
	)
	require.NoError(t, err)
	require.Len(t, client.addRequests, 1)
	require.Empty(t, client.setRequests)
	require.Equal(
		t,
		"./definitions/support.json",
		client.addRequests[0].GetService().GetAdditionalProperties().
			GetFields()["$ref"].GetStringValue(),
	)
}

func TestAgentServiceAddCommand_UpdatesCompleteServiceAndPreservesTemplates(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAgentServiceAddProject(t, projectRoot, `  ai-project:
    host: azure.ai.project
    project: .
  support-agent:
    host: azure.ai.agent
    project: ${AGENT_PROJECT}
    uses:
      - ai-project
      - manual-tool
    env:
      API_KEY: ${AGENT_API_KEY}
    customField: ${CUSTOM_VALUE}
    $ref: ./definitions/old.yaml
`)
	writeAgentServiceAddDefinition(t, projectRoot, "definitions/support.yaml")

	client := &recordingAgentServiceAddClient{projectPath: projectRoot}
	stdout, stderr, err := executeAgentServiceAddWithRecordingClient(
		t,
		client,
		"support-agent",
		"--file", "./definitions/support.yaml",
		"--output", "json",
	)
	require.NoError(t, err)
	require.Len(t, client.setRequests, 1)
	require.Empty(t, client.addRequests)

	update := client.setRequests[0]
	require.Equal(t, "support-agent", update.GetServiceName())
	require.Empty(t, update.GetPath())
	fields := update.GetSection().GetFields()
	require.Equal(t, "${AGENT_PROJECT}", fields["project"].GetStringValue())
	require.Equal(t, "${CUSTOM_VALUE}", fields["customField"].GetStringValue())
	require.Equal(
		t,
		"${AGENT_API_KEY}",
		fields["env"].GetStructValue().GetFields()["API_KEY"].GetStringValue(),
	)
	require.Equal(
		t,
		"./definitions/support.yaml",
		fields["$ref"].GetStringValue(),
	)
	require.Equal(
		t,
		[]any{"ai-project", "manual-tool"},
		fields["uses"].GetListValue().AsSlice(),
	)
	require.Contains(t, stderr, "could not safely parse the existing agent definition")

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, map[string]any{
		"name":         "support-agent",
		"host":         AiAgentHost,
		"mutation":     "updated",
		"ref":          "./definitions/support.yaml",
		"project":      "${AGENT_PROJECT}",
		"dependencies": []any{"ai-project", "manual-tool"},
	}, result)
	assert.NotContains(t, stdout, "Warning:")
}

func TestAgentServiceAddCommand_UnchangedDoesNotWrite(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAgentServiceAddProject(t, projectRoot, `  ai-project:
    host: azure.ai.project
    project: .
  support-agent:
    host: azure.ai.agent
    project: definitions
    uses:
      - ai-project
    $ref: ./definitions/support.yaml
`)
	writeAgentServiceAddDefinition(t, projectRoot, "definitions/support.yaml")

	client := &recordingAgentServiceAddClient{projectPath: projectRoot}
	stdout, stderr, err := executeAgentServiceAddWithRecordingClient(
		t,
		client,
		"support-agent",
		"--file", "./definitions/support.yaml",
		"--output", "json",
	)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Empty(t, client.addRequests)
	require.Empty(t, client.setRequests)

	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &result))
	assert.Equal(t, "unchanged", result["mutation"])
}

func TestAgentServiceAddCommand_ValidationErrorsDoNotWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		services string
		flags    []string
		want     string
	}{
		{
			name: "missing Project",
			services: `  app:
    host: containerapp
`,
			want: "no azure.ai.project service",
		},
		{
			name: "multiple Projects despite explicit selector",
			services: `  ai-project:
    host: azure.ai.project
  other-project:
    host: azure.ai.project
`,
			flags: []string{"--project", "ai-project"},
			want:  "multiple azure.ai.project services",
		},
		{
			name: "project selector mismatch",
			services: `  ai-project:
    host: azure.ai.project
`,
			flags: []string{"--project", "other-project"},
			want:  "does not match the sole azure.ai.project service",
		},
		{
			name: "non-Agent service collision",
			services: `  ai-project:
    host: azure.ai.project
  support-agent:
    host: containerapp
`,
			want: `has host "containerapp"`,
		},
		{
			name: "empty explicit source",
			services: `  ai-project:
    host: azure.ai.project
`,
			flags: []string{"--source", ""},
			want:  "--source must name a project directory",
		},
		{
			name: "source outside project",
			services: `  ai-project:
    host: azure.ai.project
`,
			flags: []string{"--source", "../outside"},
			want:  "invalid project source",
		},
		{
			name: "missing definition file",
			services: `  ai-project:
    host: azure.ai.project
`,
			flags: []string{"--file", "./missing.yaml"},
			want:  "was not found",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			projectRoot := t.TempDir()
			writeAgentServiceAddProject(t, projectRoot, test.services)
			writeAgentServiceAddDefinition(t, projectRoot, "definitions/support.yaml")
			client := &recordingAgentServiceAddClient{projectPath: projectRoot}
			flags := append(
				[]string{"--file", "./definitions/support.yaml"},
				test.flags...,
			)

			_, _, err := executeAgentServiceAddWithRecordingClient(
				t,
				client,
				append([]string{"support-agent"}, flags...)...,
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.want)
			assert.Empty(t, client.addRequests)
			assert.Empty(t, client.setRequests)
		})
	}
}

func TestAgentServiceAddCommand_RequiresFileBeforeCreatingClient(t *testing.T) {
	t.Parallel()

	clientFactoryCalls := 0
	_, _, err := executeAgentServiceAddCommand(
		t,
		func() (azdext.ProjectServiceClient, func(), error) {
			clientFactoryCalls++
			return &recordingAgentServiceAddClient{}, nil, nil
		},
		nil,
		"support-agent",
	)
	require.ErrorContains(t, err, `required flag(s) "file" not set`)
	assert.Zero(t, clientFactoryCalls)
}

func TestAgentServiceAddCommand_PreservesCancellationAndRPCFailures(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAgentServiceAddProject(t, projectRoot, `  ai-project:
    host: azure.ai.project
`)
	writeAgentServiceAddDefinition(t, projectRoot, "definitions/support.yaml")

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()
		client := &recordingAgentServiceAddClient{projectPath: projectRoot}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, _, err := executeAgentServiceAddCommand(
			t,
			agentServiceAddFactory(client),
			ctx,
			"support-agent",
			"--file", "./definitions/support.yaml",
		)
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, client.addRequests)
		assert.Empty(t, client.setRequests)
	})

	t.Run("Get error", func(t *testing.T) {
		t.Parallel()
		expected := errors.New("get failed")
		client := &recordingAgentServiceAddClient{
			projectPath: projectRoot,
			getErr:      expected,
		}

		_, _, err := executeAgentServiceAddWithRecordingClient(
			t,
			client,
			"support-agent",
			"--file", "./definitions/support.yaml",
		)
		require.ErrorIs(t, err, expected)
		assert.Empty(t, client.addRequests)
		assert.Empty(t, client.setRequests)
	})

	t.Run("AddService error", func(t *testing.T) {
		t.Parallel()
		expected := errors.New("add failed")
		client := &recordingAgentServiceAddClient{
			projectPath: projectRoot,
			addErr:      expected,
		}

		_, _, err := executeAgentServiceAddWithRecordingClient(
			t,
			client,
			"support-agent",
			"--file", "./definitions/support.yaml",
		)
		require.ErrorIs(t, err, expected)
		require.Len(t, client.addRequests, 1)
		assert.Empty(t, client.setRequests)
	})

	t.Run("SetServiceConfigSection error", func(t *testing.T) {
		t.Parallel()
		updateRoot := t.TempDir()
		writeAgentServiceAddProject(t, updateRoot, `  ai-project:
    host: azure.ai.project
  support-agent:
    host: azure.ai.agent
    project: old-source
    $ref: ./definitions/old.yaml
`)
		writeAgentServiceAddDefinition(t, updateRoot, "definitions/support.yaml")
		expected := errors.New("update failed")
		client := &recordingAgentServiceAddClient{
			projectPath: updateRoot,
			setErr:      expected,
		}

		_, _, err := executeAgentServiceAddWithRecordingClient(
			t,
			client,
			"support-agent",
			"--file", "./definitions/support.yaml",
		)
		require.ErrorIs(t, err, expected)
		require.Len(t, client.setRequests, 1)
		assert.Empty(t, client.addRequests)
	})
}

func TestAgentServiceAddHumanOutputDistinguishesMutations(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		mutation agentServiceMutation
		want     string
	}{
		{mutation: agentServiceMutationAdded, want: "Added Agent service"},
		{mutation: agentServiceMutationUpdated, want: "Updated Agent service"},
		{mutation: agentServiceMutationUnchanged, want: "is unchanged"},
	} {
		t.Run(string(test.mutation), func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			err := writeAgentServiceAddHumanResult(&out, agentServiceAddResult{
				Name:     "support-agent",
				Mutation: string(test.mutation),
			})
			require.NoError(t, err)
			assert.Contains(t, out.String(), test.want)
			assert.Contains(t, out.String(), "azd deploy support-agent")
			assert.Contains(t, out.String(), "azd up")
		})
	}
}

type agentServiceAddFileServer struct {
	azdext.UnimplementedProjectServiceServer

	projectRoot string
	setRequests []*azdext.SetServiceConfigSectionRequest
}

func (s *agentServiceAddFileServer) Get(
	_ context.Context,
	_ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	return &azdext.GetProjectResponse{
		Project: &azdext.ProjectConfig{Path: s.projectRoot},
	}, nil
}

func (s *agentServiceAddFileServer) SetServiceConfigSection(
	_ context.Context,
	request *azdext.SetServiceConfigSectionRequest,
) (*azdext.EmptyResponse, error) {
	s.setRequests = append(s.setRequests, proto.Clone(request).(*azdext.SetServiceConfigSectionRequest))
	if request.GetPath() != "" {
		return nil, fmt.Errorf("expected a complete service section update")
	}

	projectFile := filepath.Join(s.projectRoot, "azure.yaml")
	data, err := os.ReadFile(projectFile)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	services, ok := document["services"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("project file has no services mapping")
	}
	services[request.GetServiceName()] = request.GetSection().AsMap()
	updated, err := yaml.Marshal(document)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(projectFile, updated, 0o600); err != nil {
		return nil, err
	}
	return &azdext.EmptyResponse{}, nil
}

func TestAgentServiceAddCommand_PersistsThroughProjectServiceClient(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	writeAgentServiceAddProject(t, projectRoot, `  ai-project:
    host: azure.ai.project
    project: .
  support-agent:
    host: azure.ai.agent
    project: ${AGENT_PROJECT}
    uses:
      - ai-project
      - manual-tool
    env:
      API_KEY: ${AGENT_API_KEY}
    customField: ${CUSTOM_VALUE}
    $ref: ./definitions/old.yaml
`)
	writeAgentServiceAddDefinition(t, projectRoot, "definitions/support.yaml")

	server := &agentServiceAddFileServer{projectRoot: projectRoot}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	azdext.RegisterProjectServiceServer(grpcServer, server)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(grpcServer.Stop)

	connection, err := grpc.NewClient(
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, connection.Close())
	})
	client := azdext.NewProjectServiceClient(connection)

	stdout, _, err := executeAgentServiceAddCommand(
		t,
		func() (azdext.ProjectServiceClient, func(), error) {
			return client, nil, nil
		},
		nil,
		"support-agent",
		"--file", "./definitions/support.yaml",
		"--output", "json",
	)
	require.NoError(t, err)
	require.Len(t, server.setRequests, 1)
	require.Empty(t, server.setRequests[0].GetPath())

	data, err := os.ReadFile(filepath.Join(projectRoot, "azure.yaml"))
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal(data, &document))
	services, ok := document["services"].(map[string]any)
	require.True(t, ok)
	agent, ok := services["support-agent"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "${AGENT_PROJECT}", agent["project"])
	require.Equal(t, "${CUSTOM_VALUE}", agent["customField"])
	require.Equal(t, "./definitions/support.yaml", agent["$ref"])
	require.Equal(t, []any{"ai-project", "manual-tool"}, agent["uses"])
	environment, ok := agent["env"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "${AGENT_API_KEY}", environment["API_KEY"])
	assert.True(t, strings.Contains(stdout, `"mutation": "updated"`))
}
