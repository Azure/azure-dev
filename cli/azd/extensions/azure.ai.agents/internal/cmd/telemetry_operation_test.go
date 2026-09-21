// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	agentTelemetry "azureaiagent/internal/telemetry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type operationRecordingReporter struct {
	mu     sync.Mutex
	events []foundryTelemetry.Event
}

func (r *operationRecordingReporter) Report(ctx context.Context, event foundryTelemetry.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func TestOperationReporterDeduplicatesPerOperation(t *testing.T) {
	t.Parallel()
	r := newOperationReporter()
	capture := &operationRecordingReporter{}
	classes := []agentTelemetry.OperationClass{{Category: "hosted", Telephony: "none"}}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			r.report(t.Context(), capture, "provision", classes)
			r.report(t.Context(), capture, "deploy", classes)
		})
	}
	wg.Wait()
	require.Len(t, capture.events, 2)
	require.NotEqual(t, capture.events[0].Name, capture.events[1].Name)
	for _, event := range capture.events {
		require.Empty(t, event.Attributes)
	}
	// Unsupported operations do not report and nil reporters are harmless.
	r.report(t.Context(), capture, "down", classes)
	r.report(t.Context(), nil, "init", classes)
	require.Len(t, capture.events, 2)
}

func TestInitOperationRefinesIntentWithoutChangingContext(t *testing.T) {
	t.Parallel()
	ctx := withInitOperationContext(t.Context(), "hosted", false)
	recordInitDefinition(ctx, agent_yaml.VoiceAgent{
		AgentDefinition: agent_yaml.AgentDefinition{Kind: agent_yaml.AgentKindVoice, Name: "private-name"},
		ModelType:       agent_yaml.VoiceModelTypeSelfDeployed,
	})
	state, ok := ctx.Value(initOperationContextKey{}).(*initOperationContext)
	require.True(t, ok)
	require.Equal(t, []agentTelemetry.OperationClass{{Category: "voice_byom", Telephony: "none"}}, state.classes)
	recordInitProjectContent(ctx, []byte(`services:
  secret-target:
    host: azure.ai.agent
    kind: hosted
    protocols: [{protocol: invocations_ws}]
  secret-wrapper:
    host: azure.ai.agent
    kind: voice
    conversationEngine: {type: hosted_agent, name: secret-target}
`))
	require.ElementsMatch(t, []agentTelemetry.OperationClass{
		{Category: "hosted_invocations_ws", Telephony: "none"},
		{Category: "voice_hosted_wrapper", Telephony: "none"},
	}, state.classes)
	unknown := withInitOperationContext(t.Context(), "hosted", true)
	require.Empty(t, unknown.Value(initOperationContextKey{}).(*initOperationContext).classes)
	recordInitProperties(t.Context(), map[string]any{"kind": "voice"}) // no state: no-op
	recordInitProjectContent(ctx, []byte("[invalid"))                  // telemetry parsing never surfaces an error
	require.Len(t, state.classes, 2)
}

func TestOperationProjectClassDoesNotReadDefinitions(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{"kind": "voice"})
	require.NoError(t, err)
	svc := &azdext.ServiceConfig{Host: AiAgentHost, AdditionalProperties: props}
	t.Setenv("AGENT_DEFINITION_PATH", "missing-sensitive-file.yaml")
	require.Equal(t, "unknown", operationServiceClass(svc).Category)
	t.Setenv("AGENT_DEFINITION_PATH", " \t ")
	require.Equal(t, "unknown", operationServiceClass(svc).Category)
	t.Setenv("AGENT_DEFINITION_PATH", "")
	require.Equal(t, "voice_managed", operationServiceClass(svc).Category)
	require.Empty(t, operationProjectClasses(nil))
	ref, err := structpb.NewStruct(map[string]any{"$ref": "missing-sensitive-file.yaml"})
	require.NoError(t, err)
	svc.AdditionalProperties = ref
	require.Equal(t, "unknown", operationServiceClass(svc).Category)
	// Reuse uses only the project already loaded by the existing flow.
	ctx := withInitOperationContext(t.Context(), "", false)
	recordInitProject(ctx, &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"agent": {Host: AiAgentHost, AdditionalProperties: props},
		"web":   {Host: "containerapp"},
	}})
	state, ok := ctx.Value(initOperationContextKey{}).(*initOperationContext)
	require.True(t, ok)
	require.Equal(t, []agentTelemetry.OperationClass{{Category: "voice_managed", Telephony: "none"}}, state.classes)
}

func TestInitOperationProjectContentPropertyPrecedence(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")
	for _, tt := range []struct {
		name, service, want string
	}{
		{"legacy", "config: {kind: voice, modelType: self_deployed}", "voice_byom"},
		{"legacy-with-unrelated-inline", "custom: private-value\n    config: {kind: prompt}", "prompt"},
		{"inline-wins", "kind: hosted\n    config: {kind: voice}", "hosted"},
		{"missing-kind", "custom: private-value", "unknown"},
		{"unresolved-ref", "kind: voice\n    $ref: private-path", "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := withInitOperationContext(t.Context(), "", true)
			content := []byte("services:\n  agent:\n    host: azure.ai.agent\n    " + tt.service + "\n")
			before := bytes.Clone(content)
			recordInitProjectContent(ctx, content)
			state := ctx.Value(initOperationContextKey{}).(*initOperationContext)
			require.Len(t, state.classes, 1)
			require.Equal(t, tt.want, state.classes[0].Category)
			require.Equal(t, before, content)
		})
	}
	t.Setenv("AGENT_DEFINITION_PATH", "missing-private-definition.yaml")
	ctx := withInitOperationContext(t.Context(), "", true)
	recordInitProjectContent(ctx, []byte("services:\n  agent:\n    host: azure.ai.agent\n    kind: voice\n"))
	state := ctx.Value(initOperationContextKey{}).(*initOperationContext)
	require.Equal(t, []agentTelemetry.OperationClass{{Category: "unknown", Telephony: "unknown"}}, state.classes)
}

func TestOperationMarkerDoesNotChangeOriginalContextContract(t *testing.T) {
	t.Parallel()
	props, err := structpb.NewStruct(map[string]any{
		"kind": "voice", "modelType": "self_deployed", "instructions": "private instructions",
		"telephony": map[string]any{"bindings": []any{map[string]any{"identifier": "private number"}}},
	})
	require.NoError(t, err)
	project := &azdext.ProjectConfig{Services: map[string]*azdext.ServiceConfig{
		"private-name": {Host: AiAgentHost, AdditionalProperties: props},
	}}
	before, err := json.Marshal(project)
	require.NoError(t, err)
	original := &telemetryRecordingClient{}
	r := newAgentContextReporter()
	r.reportProjectConfig(t.Context(), original, project, "provision")
	additional := &operationRecordingReporter{}
	newOperationReporter().report(t.Context(), additional, "provision", operationProjectClasses(project))
	r.reportProjectConfig(t.Context(), original, project, "deploy")
	require.Len(t, original.requests, 1, "existing process-wide deduplication must not change")
	require.Equal(t, agentContextResolvedEvent, original.requests[0].EventName)
	require.Equal(t, map[string]string{
		agentKindAttribute: "voice", agentHarnessAttribute: "none", agentOperationAttribute: "provision",
	}, original.requests[0].Attributes)
	require.Len(t, additional.events, 1)
	require.Nil(t, additional.events[0].Attributes)
	require.Equal(t, "agent.operation.v1.provision.voice_byom.enabled", additional.events[0].Name)
	after, err := json.Marshal(project)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestOperationServiceClassPropertyPrecedence(t *testing.T) {
	t.Setenv("AGENT_DEFINITION_PATH", "")
	for _, tt := range []struct {
		name           string
		inline, legacy map[string]any
		want           string
	}{
		{"legacy-with-unrelated-inline", map[string]any{"custom": "private-value"},
			map[string]any{"kind": "voice", "modelType": "self_deployed"}, "voice_byom"},
		{"inline-kind-wins", map[string]any{"kind": "hosted"},
			map[string]any{"kind": "voice"}, "hosted"},
		{"legacy-only", nil, map[string]any{"kind": "prompt"}, "prompt"},
		{"no-kind", map[string]any{"custom": "private-value"}, nil, "unknown"},
		{"unresolved-ref", map[string]any{"kind": "voice", "$ref": "private-path"},
			map[string]any{"kind": "prompt"}, "unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			inline, err := structpb.NewStruct(tt.inline)
			require.NoError(t, err)
			legacy, err := structpb.NewStruct(tt.legacy)
			require.NoError(t, err)
			svc := &azdext.ServiceConfig{Host: AiAgentHost, AdditionalProperties: inline, Config: legacy}
			before, err := json.Marshal(svc)
			require.NoError(t, err)
			require.Equal(t, tt.want, operationServiceClass(svc).Category)
			after, err := json.Marshal(svc)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

type operationTelemetryServer struct {
	azdext.UnimplementedTelemetryServiceServer
	azdext.UnimplementedProjectServiceServer
	mu          sync.Mutex
	events      []*azdext.ReportUsageRequest
	err         error
	block       bool
	traceparent string
}

func (s *operationTelemetryServer) Get(
	ctx context.Context, req *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	props, err := structpb.NewStruct(map[string]any{"kind": "hosted"})
	if err != nil {
		return nil, err
	}
	return &azdext.GetProjectResponse{Project: &azdext.ProjectConfig{
		Services: map[string]*azdext.ServiceConfig{
			"agent": {Host: AiAgentHost, AdditionalProperties: props},
		},
	}}, nil
}

func (s *operationTelemetryServer) ReportUsage(
	ctx context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, req)
	md, _ := metadata.FromIncomingContext(ctx)
	if values := md.Get("traceparent"); len(values) > 0 {
		s.traceparent = values[0]
	}
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &azdext.ReportUsageResponse{Accepted: false}, s.err
}

func TestFailedInitReportsIntentWithoutChangingFailure(t *testing.T) {
	for _, tt := range []struct {
		name  string
		err   error
		block bool
	}{
		{"not-accepted", nil, false},
		{"rpc-failure", errors.New("sensitive transport text"), false},
		{"old-host", status.Error(codes.Unimplemented, "unavailable"), false},
		{"deadline", nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := grpc.NewServer()
			capture := &operationTelemetryServer{err: tt.err, block: tt.block}
			azdext.RegisterTelemetryServiceServer(server, capture)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			t.Setenv("AZD_SERVER", listener.Addr().String())
			cmd := newInitCommand(nil)
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{"--kind", "prompt-voice", "--runtime", "python_3_13"})
			start := time.Now()
			err = cmd.Execute()
			require.Less(t, time.Since(start), 3*time.Second, "telemetry must have a bounded delay and no retries")
			require.ErrorContains(t, err, "new prompt voice agents cannot use these init inputs")
			capture.mu.Lock()
			defer capture.mu.Unlock()
			require.Len(t, capture.events, 1)
			require.Equal(t, "agent.operation.v1.init.voice_managed.none", capture.events[0].EventName)
			require.Empty(t, capture.events[0].Attributes)
			require.NotContains(t, output.String(), "sensitive transport text")
		})
	}
}

func TestOperationReporterHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	capture := &operationRecordingReporter{}
	newOperationReporter().report(ctx, capture, "deploy", nil)
	require.Empty(t, capture.events)
	start := time.Now()
	reportInitOperation(ctx) // no state: no connection or wait
	require.Less(t, time.Since(start), time.Second)
}

func TestInitOperationReportsAfterCancellationWithoutChangingResult(t *testing.T) {
	server := grpc.NewServer()
	capture := &operationTelemetryServer{err: status.Error(codes.Unavailable, "private transport detail")}
	azdext.RegisterTelemetryServiceServer(server, capture)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	t.Setenv("AZD_SERVER", listener.Addr().String())
	const parent = "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	t.Setenv("TRACEPARENT", parent)
	ctx, cancel := context.WithCancel(azdext.WithAccessToken(t.Context()))
	ctx = withInitOperationContext(ctx, "prompt", false)
	cancel()
	original := errors.New("original business failure")
	run := func() error {
		defer reportInitOperation(ctx)
		return original
	}
	require.Same(t, original, run())
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	capture.mu.Lock()
	defer capture.mu.Unlock()
	require.Len(t, capture.events, 1)
	require.Equal(t, "agent.operation.v1.init.prompt.none", capture.events[0].EventName)
	require.Empty(t, capture.events[0].Attributes)
	require.Equal(t, parent, capture.traceparent)
}

func TestInitOperationSuccessPreservesOriginalEventPriority(t *testing.T) {
	server := grpc.NewServer()
	capture := &operationTelemetryServer{}
	azdext.RegisterTelemetryServiceServer(server, capture)
	azdext.RegisterProjectServiceServer(server, capture)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	t.Setenv("AZD_SERVER", listener.Addr().String())
	root := NewRootCommand()
	initCmd, _, err := root.Find([]string{"init"})
	require.NoError(t, err)
	initCmd.SetContext(withInitOperationContext(t.Context(), "hosted", false))
	root.PersistentPostRun(initCmd, nil)
	capture.mu.Lock()
	defer capture.mu.Unlock()
	require.Len(t, capture.events, 2)
	require.Equal(t, agentContextResolvedEvent, capture.events[0].EventName)
	require.Equal(t, "agent.operation.v1.init.hosted.none", capture.events[1].EventName)
	require.Empty(t, capture.events[1].Attributes)
}
