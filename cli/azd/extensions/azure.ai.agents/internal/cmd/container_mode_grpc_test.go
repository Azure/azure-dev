// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type containerModeRPCRecorder struct {
	azdext.UnimplementedTelemetryServiceServer
	mu       sync.Mutex
	requests []*azdext.ReportUsageRequest
}

func (r *containerModeRPCRecorder) ReportUsage(
	_ context.Context, request *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, proto.Clone(request).(*azdext.ReportUsageRequest))
	// Locally built extensions can send usage requests even when recording is gated off.
	return &azdext.ReportUsageResponse{Accepted: false}, nil
}

func (r *containerModeRPCRecorder) snapshot() []*azdext.ReportUsageRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*azdext.ReportUsageRequest(nil), r.requests...)
}

func TestAgentContainerModeRequestOverGRPC(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "worker", "registryConnectionId": "secret-connection",
		"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
	})
	require.NoError(t, err)
	projectServer := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
			"worker": {
				Name: "worker", Host: AiAgentHost, Image: "private.example.com/team/agent:secret-tag",
				AdditionalProperties: props, Docker: &azdext.DockerProjectOptions{ImagePassthrough: true},
			},
		},
	}}
	server := grpc.NewServer()
	recorder := &containerModeRPCRecorder{}
	azdext.RegisterTelemetryServiceServer(server, recorder)
	azdext.RegisterProjectServiceServer(server, projectServer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())
	t.Setenv("AZD_ACCESS_TOKEN", "test-token")
	t.Setenv("NO_COLOR", "1")

	root := NewRootCommand()
	root.SetArgs([]string{"invoke", "--protocol", "invocations"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	// Input validation fails after the root pre-run reports the project context.
	require.ErrorContains(t, root.ExecuteContext(t.Context()), "a message argument or --input-file is required")

	requests := recorder.snapshot()
	require.Len(t, requests, 1)
	require.Equal(t, "agent.context.resolved", requests[0].GetEventName())
	require.Equal(t, map[string]string{
		"agent.kind": "hosted", "agent.harness": "none",
		"agent.operation": "invoke", "agent.container.mode": "passthrough_auth",
	}, requests[0].GetAttributes())

	for _, tt := range []struct {
		args      []string
		operation string
	}{
		{args: []string{"optimize", "apply", "--candidate", "test-candidate", "-p", testProjectEndpoint},
			operation: "optimize.apply"},
		{args: []string{"show", "--project-endpoint", testProjectEndpoint}},
		{args: []string{"monitor", "--agent-endpoint", "test-agent-endpoint"}},
		{args: []string{"sessions", "list", "--project-endpoint", testProjectEndpoint}},
		{args: []string{"sessions", "show", "--agent-endpoint", "test-agent-endpoint"}},
		{args: []string{"files", "ls", "--project-endpoint", testProjectEndpoint}},
		{args: []string{"files", "download", "/data", "--agent-endpoint", "test-agent-endpoint"}},
		{args: []string{"show"}, operation: "show"},
	} {
		t.Run(tt.args[0], func(t *testing.T) {
			root := NewRootCommand()
			cmd, _, err := root.Find(tt.args)
			require.NoError(t, err)
			// Exercise the real parsing and pre-run hook without performing any command action.
			cmd.Args = nil
			cmd.RunE = func(*cobra.Command, []string) error { return nil }
			root.SetArgs(tt.args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			before := len(recorder.snapshot())

			require.NoError(t, root.ExecuteContext(t.Context()))
			reported := recorder.snapshot()[before:]
			if tt.operation == "" {
				require.Empty(t, reported)
				return
			}
			require.Len(t, reported, 1)
			require.Equal(t, "agent.context.resolved", reported[0].GetEventName())
			require.Equal(t, tt.operation, reported[0].GetAttributes()["agent.operation"])
		})
	}
}
