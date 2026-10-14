// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type invokeUsageFailingCredential struct{}

func (invokeUsageFailingCredential) GetToken(
	_ context.Context, _ policy.TokenRequestOptions,
) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, errors.New("test credential unavailable")
}

func TestInvokeResolverClassifiesHostedKindForTelemetry(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")
	for _, tt := range []struct {
		kind       string
		wantHosted bool
	}{
		{"hosted", true}, {"prompt", false}, {"voice", false}, {"workflow", false},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			props, err := structpb.NewStruct(map[string]any{
				"kind": tt.kind, "name": "worker",
				"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
			})
			require.NoError(t, err)
			projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"worker": {Name: "worker", Host: AiAgentHost, AdditionalProperties: props},
				},
			}}
			client := newHelpersTestAzdClient(t, projectServer, &helpersPromptServer{})
			info, err := resolveAgentServiceFromProject(t.Context(), client, "worker", true, withHostedKind())
			require.NoError(t, err)
			require.Equal(t, tt.wantHosted, info.IsHosted)
		})
	}
}

func TestInvokeUsageReportsSelectedRemoteModeBeforeAuthentication(t *testing.T) {
	for _, tt := range []struct {
		name        string
		protocol    agent_api.AgentProtocol
		longRunning bool
		noWait      bool
		want        map[string]string
	}{
		{"responses foreground", agent_api.AgentProtocolResponses, false, false,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "false",
				"agent.invoke.no_wait": "false",
			}},
		{"responses attached", agent_api.AgentProtocolResponses, true, false,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "true",
				"agent.invoke.no_wait": "false",
			}},
		{"responses detached", agent_api.AgentProtocolResponses, true, true,
			map[string]string{
				"agent.invoke.protocol": "responses", "agent.invoke.long_running": "true",
				"agent.invoke.no_wait": "true",
			}},
		{"invocations", agent_api.AgentProtocolInvocations, false, false,
			map[string]string{
				"agent.invoke.protocol": "invocations", "agent.invoke.long_running": "false",
				"agent.invoke.no_wait": "false",
			}},
		{"a2a", agent_api.AgentProtocolA2A, false, false,
			map[string]string{
				"agent.invoke.protocol": "a2a", "agent.invoke.long_running": "false",
				"agent.invoke.no_wait": "false",
			}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &telemetryRecordingClient{}
			action := &InvokeAction{
				flags: &invokeFlags{
					protocol: string(tt.protocol), message: "private prompt",
					longRunning: tt.longRunning, noWait: tt.noWait,
				},
				endpoint:              &parsedAgentEndpoint{}, // bypass prompt-agent routing
				resolvedRemoteContext: &remoteContext{name: "test"},
				credential:            invokeUsageFailingCredential{},
				invokeReporter:        foundryTelemetry.NewReporter(client, nil),
			}

			// The invoke fails during authentication, after the usage event but before any service request.
			require.Error(t, action.Run(t.Context()))
			require.Len(t, client.requests, 1)
			require.Equal(t, "agent.invoke.selected", client.requests[0].EventName)
			require.Equal(t, tt.want, client.requests[0].Attributes)
		})
	}
}

func TestInvokeUsageReportsHostedProjectRoute(t *testing.T) {
	client := &telemetryRecordingClient{}
	action := &InvokeAction{
		flags:                 &invokeFlags{protocol: "invocations", message: "private prompt"},
		resolvedRemoteContext: &remoteContext{name: "worker", serviceName: "worker", hosted: true},
		credential:            invokeUsageFailingCredential{},
		invokeReporter:        foundryTelemetry.NewReporter(client, nil),
	}

	require.ErrorContains(t, action.Run(t.Context()), "failed to get auth token")
	require.Len(t, client.requests, 1)
	require.Equal(t, "agent.invoke.selected", client.requests[0].EventName)
}

func TestInvokeUsageSkipsInvalidResolvedMode(t *testing.T) {
	client := &telemetryRecordingClient{}
	action := &InvokeAction{
		flags:          &invokeFlags{protocol: "invocations", longRunning: true},
		endpoint:       &parsedAgentEndpoint{},
		invokeReporter: foundryTelemetry.NewReporter(client, nil),
	}

	require.ErrorContains(t, action.Run(t.Context()), "--long-running is not supported")
	require.Empty(t, client.requests)
}

func TestInvokeUsageSkipsLocalInvokeAndInvalidInput(t *testing.T) {
	for _, tt := range []struct {
		name  string
		flags invokeFlags
	}{
		{"local route", invokeFlags{protocol: "a2a", local: true}},
		{"unreadable input", invokeFlags{protocol: "invocations", inputFile: "missing-input-file"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &telemetryRecordingClient{}
			action := &InvokeAction{
				flags:          &tt.flags,
				endpoint:       &parsedAgentEndpoint{},
				invokeReporter: foundryTelemetry.NewReporter(client, nil),
			}
			require.Error(t, action.Run(t.Context()))
			require.Empty(t, client.requests)
		})
	}
}

func TestInvokeUsageReportingFailureDoesNotChangeInvokeError(t *testing.T) {
	client := &telemetryRecordingClient{err: errors.New("telemetry unavailable")}
	action := &InvokeAction{
		flags:                 &invokeFlags{protocol: "invocations", message: "private prompt"},
		endpoint:              &parsedAgentEndpoint{},
		resolvedRemoteContext: &remoteContext{name: "test"},
		credential:            invokeUsageFailingCredential{},
		invokeReporter:        foundryTelemetry.NewReporter(client, nil),
	}

	require.ErrorContains(t, action.Run(t.Context()), "failed to get auth token")
	require.Len(t, client.requests, 1)
}
