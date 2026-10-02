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
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type containerModeRPCRecorder struct {
	v1beta.UnimplementedTelemetryServiceServer
	mu       sync.Mutex
	requests []*v1beta.ReportUsageRequest
}

func (r *containerModeRPCRecorder) ReportUsage(
	_ context.Context, request *v1beta.ReportUsageRequest,
) (*v1beta.ReportUsageResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, proto.Clone(request).(*v1beta.ReportUsageRequest))
	// Locally built extensions can send usage requests even when recording is gated off.
	return &v1beta.ReportUsageResponse{Accepted: false}, nil
}

func (r *containerModeRPCRecorder) snapshot() []*v1beta.ReportUsageRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*v1beta.ReportUsageRequest(nil), r.requests...)
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
	v1beta.RegisterTelemetryServiceServer(server, recorder)
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
}
