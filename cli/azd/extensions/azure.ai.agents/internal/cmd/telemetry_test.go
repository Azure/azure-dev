// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"maps"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"
)

type telemetryRecordingClient struct {
	requests []*azdext.ReportUsageRequest
	err      error
}

func (c *telemetryRecordingClient) ReportUsage(
	_ context.Context,
	request *azdext.ReportUsageRequest,
	_ ...grpc.CallOption,
) (*azdext.ReportUsageResponse, error) {
	c.requests = append(c.requests, request)
	return &azdext.ReportUsageResponse{Accepted: true}, c.err
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
	hostedProps, err := structpb.NewStruct(map[string]any{"kind": "hosted", "name": "worker"})
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
	require.Equal(t, "invoke", byKind["prompt"].operation)
}

func TestAgentContextReporterReportsContext(t *testing.T) {
	reporter := newAgentContextReporter()
	client := &telemetryRecordingClient{}
	agentCtx := agentTelemetryContext{
		kind: "hosted", harness: agentHarnessNone, operation: "deploy",
	}

	reporter.report(t.Context(), client, agentCtx)

	require.Len(t, client.requests, 1)
	require.Equal(t, agentContextResolvedEvent, client.requests[0].EventName)
	require.True(t, maps.Equal(client.requests[0].Attributes, map[string]string{
		agentKindAttribute:      "hosted",
		agentHarnessAttribute:   agentHarnessNone,
		agentOperationAttribute: "deploy",
	}))
}

func TestAgentContextReporterCollapsesDuplicateClassifications(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{"kind": "hosted"})
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

func TestTelemetryClassificationsBoundCustomerValues(t *testing.T) {
	require.Equal(t, agentKindUnknown, telemetryAgentKind("customer-kind"))
	require.Equal(t, agentHarnessOther, telemetryAgentHarness("customer-harness"))
	require.Equal(t, "files.upload", telemetryOperation("agent files upload"))
	require.Equal(t, string(agent_yaml.AgentKindVoice), telemetryAgentKind(string(agent_yaml.AgentKindVoice)))
}
