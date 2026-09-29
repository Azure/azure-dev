// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type telemetryRecordingClient struct {
	requests []*v1beta.ReportUsageRequest
	err      error
}

func (c *telemetryRecordingClient) ReportUsage(
	_ context.Context,
	request *v1beta.ReportUsageRequest,
	_ ...grpc.CallOption,
) (*v1beta.ReportUsageResponse, error) {
	c.requests = append(c.requests, request)
	return &v1beta.ReportUsageResponse{Accepted: true}, c.err
}

func TestAgentTelemetryContextsClassifiesKindsAndHarnesses(t *testing.T) {
	promptProps, err := structpb.NewStruct(map[string]any{
		"kind":         "prompt",
		"name":         "assistant",
		"model":        "gpt",
		"instructions": "help",
		"harness":      map[string]any{"type": "github_copilot_preview"},
	})
	require.NoError(t, err)
	hostedProps, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "worker", "protocols": []any{map[string]any{
			"protocol": "responses", "version": "1.0.0",
		}},
	})
	require.NoError(t, err)

	contexts := agentTelemetryContexts(&azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"assistant": {Name: "assistant", Host: AiAgentHost, AdditionalProperties: promptProps},
			"worker":    {Name: "worker", Host: AiAgentHost, AdditionalProperties: hostedProps},
			"web":       {Name: "web", Host: "containerapp"},
		},
	}, "invoke")
	require.Len(t, contexts, 2)

	byKind := map[string]agentTelemetryContext{}
	for _, agentCtx := range contexts {
		byKind[agentCtx.kind] = agentCtx
	}
	require.Equal(t, "github_copilot_preview", byKind["prompt"].harness)
	require.Equal(t, agentHarnessNone, byKind["hosted"].harness)
	require.Equal(t, containerModeBuild, byKind["hosted"].containerMode)
	require.Empty(t, byKind["prompt"].containerMode)
	require.Equal(t, "invoke", byKind["prompt"].operation)
}

func TestAgentContextReporterReportsContext(t *testing.T) {
	reporter := newAgentContextReporter()
	client := &telemetryRecordingClient{}
	agentCtx := agentTelemetryContext{
		kind: "hosted", harness: agentHarnessNone, operation: "deploy", containerMode: containerModePassthroughAuth,
	}

	reporter.report(t.Context(), client, agentCtx)

	require.Len(t, client.requests, 1)
	require.Equal(t, agentContextResolvedEvent, client.requests[0].EventName)
	require.True(t, maps.Equal(client.requests[0].Attributes, map[string]string{
		agentKindAttribute:          "hosted",
		agentHarnessAttribute:       agentHarnessNone,
		agentOperationAttribute:     "deploy",
		agentContainerModeAttribute: containerModePassthroughAuth,
	}))
}

func TestAgentContextReporterOmitsContainerModeForNonHosted(t *testing.T) {
	reporter := newAgentContextReporter()
	client := &telemetryRecordingClient{}
	reporter.report(t.Context(), client, agentTelemetryContext{
		kind: "prompt", harness: agentHarnessNone, operation: "invoke",
	})

	require.Len(t, client.requests, 1)
	require.Equal(t, map[string]string{
		agentKindAttribute: "prompt", agentHarnessAttribute: agentHarnessNone, agentOperationAttribute: "invoke",
	}, client.requests[0].Attributes)
}

func TestTelemetryContainerModeClassifiesHostedConfiguration(t *testing.T) {
	const privateImage = "private.example.com/team/agent:secret-tag"
	const connectionID = "/subscriptions/customer/registry-secret"
	tests := []struct {
		name        string
		properties  map[string]any
		image       string
		passthrough bool
		remoteBuild bool
		want        string
	}{
		{name: "remote source build", properties: map[string]any{"kind": "hosted"},
			remoteBuild: true, want: containerModeBuild},
		{name: "local build also publishes to ACR", properties: map[string]any{"kind": "hosted"},
			remoteBuild: false, want: containerModeBuild},
		{name: "code deploy", properties: map[string]any{
			"kind": "hosted", "codeConfiguration": map[string]any{
				"runtime": "python_3_13", "entryPoint": "app.py",
			},
		},
			want: containerModeCode},
		{name: "prebuilt image", properties: map[string]any{"kind": "hosted"},
			image: privateImage, passthrough: true, want: containerModePassthrough},
		{name: "prebuilt image with connection", properties: map[string]any{
			"kind": "hosted", "registryConnectionId": connectionID},
			image: privateImage, passthrough: true, want: containerModePassthroughAuth},
		{name: "legacy image without passthrough", properties: map[string]any{"kind": "hosted"},
			image: privateImage, want: containerModeUnknown},
		{name: "invalid connection without passthrough", properties: map[string]any{
			"kind": "hosted", "registryConnectionId": connectionID},
			image: privateImage, want: containerModeUnknown},
		{name: "invalid missing image", properties: map[string]any{"kind": "hosted"},
			passthrough: true, want: containerModeUnknown},
		{name: "invalid whitespace connection", properties: map[string]any{
			"kind": "hosted", "registryConnectionId": "   "},
			image: privateImage, passthrough: true, want: containerModeUnknown},
		{name: "conflicting remote build", properties: map[string]any{"kind": "hosted"},
			image: privateImage, passthrough: true, remoteBuild: true, want: containerModeUnknown},
		{name: "missing definition", want: containerModeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.properties != nil {
				tt.properties["name"] = "worker"
				tt.properties["protocols"] = []any{map[string]any{"protocol": "responses", "version": "1.0.0"}}
			}
			props, err := structpb.NewStruct(tt.properties)
			require.NoError(t, err)
			svc := &azdext.ServiceConfig{
				Name: "worker", Host: AiAgentHost, Image: tt.image, AdditionalProperties: props,
				Docker: &azdext.DockerProjectOptions{
					ImagePassthrough: tt.passthrough, RemoteBuild: tt.remoteBuild,
				},
			}
			require.Equal(t, tt.want, telemetryContainerMode(svc, t.TempDir()))
		})
	}
}

func TestTelemetryContainerModeResolvesFileRef(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(
		"kind: hosted\nname: worker\nregistryConnectionId: private-connection\n"+
			"protocols:\n  - protocol: responses\n    version: '1.0.0'\n",
	), 0o600))
	props, err := structpb.NewStruct(map[string]any{"$ref": "./agent.yaml"})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{
		Name: "worker", Host: AiAgentHost, Image: "private.example.com/team/agent:tag",
		AdditionalProperties: props, Docker: &azdext.DockerProjectOptions{ImagePassthrough: true},
	}
	require.Equal(t, containerModePassthroughAuth, telemetryContainerMode(svc, dir))
}

func TestAgentContextReporterCollapsesDuplicateClassifications(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "worker",
		"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
	})
	require.NoError(t, err)
	project := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"one": {Name: "one", Host: AiAgentHost, AdditionalProperties: props},
		"two": {Name: "two", Host: AiAgentHost, AdditionalProperties: props},
	}}
	reporter := newAgentContextReporter()
	client := &telemetryRecordingClient{}

	reporter.reportProjectConfig(t.Context(), client, project, "provision")
	reporter.reportService(t.Context(), client, project, project.Services["one"], "deploy")

	require.Len(t, client.requests, 1)
}

func TestAgentContextReporterKeepsDistinctContainerModes(t *testing.T) {
	privateProps, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "worker", "registryConnectionId": "private-connection",
		"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
	})
	require.NoError(t, err)
	buildProps, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "builder",
		"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
	})
	require.NoError(t, err)
	project := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"build": {Name: "build", Host: AiAgentHost, AdditionalProperties: buildProps},
		"private": {
			Name: "private", Host: AiAgentHost, Image: "private.example.com/team/agent:tag",
			AdditionalProperties: privateProps, Docker: &azdext.DockerProjectOptions{ImagePassthrough: true},
		},
	}}
	reporter := newAgentContextReporter()
	client := &telemetryRecordingClient{}

	reporter.reportProjectConfig(t.Context(), client, project, "provision")
	reporter.reportService(t.Context(), client, project, project.Services["private"], "deploy")

	require.Len(t, client.requests, 2)
	modes := map[string]bool{}
	for _, req := range client.requests {
		modes[req.Attributes[agentContainerModeAttribute]] = true
		require.NotContains(t, req.Attributes, "registryConnectionId")
	}
	require.Equal(t, map[string]bool{containerModeBuild: true, containerModePassthroughAuth: true}, modes)
}

func TestTelemetryClassificationsBoundCustomerValues(t *testing.T) {
	require.Equal(t, agentKindUnknown, telemetryAgentKind("customer-kind"))
	require.Equal(t, agentHarnessOther, telemetryAgentHarness("customer-harness"))
	require.Equal(t, "files.upload", telemetryOperation("agent files upload"))
	require.Equal(t, string(agent_yaml.AgentKindVoice), telemetryAgentKind(string(agent_yaml.AgentKindVoice)))
}
