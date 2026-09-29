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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

type invokeUsageRPCRecorder struct {
	v1beta.UnimplementedTelemetryServiceServer
	mu       sync.Mutex
	requests []*v1beta.ReportUsageRequest
}

func (r *invokeUsageRPCRecorder) ReportUsage(
	_ context.Context, request *v1beta.ReportUsageRequest,
) (*v1beta.ReportUsageResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, proto.Clone(request).(*v1beta.ReportUsageRequest))
	// A dev-installed extension can send the request even when the host does not record a span.
	return &v1beta.ReportUsageResponse{Accepted: false}, nil
}

func (r *invokeUsageRPCRecorder) snapshot() []*v1beta.ReportUsageRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*v1beta.ReportUsageRequest(nil), r.requests...)
}

type invokeUsageProjectThenFail struct {
	azdext.UnimplementedProjectServiceServer
	mu      sync.Mutex
	gets    int
	project *azdext.ProjectConfig
}

func (s *invokeUsageProjectThenFail) Get(
	_ context.Context, _ *azdext.EmptyRequest,
) (*azdext.GetProjectResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.gets > 2 {
		return nil, status.Error(codes.NotFound, "project unavailable after protocol selection")
	}
	return &azdext.GetProjectResponse{Project: s.project}, nil
}

func TestAgentInvokedRequestOverGRPC(t *testing.T) {
	// Exercise the real Cobra command and the gRPC client; no Azure credentials or service are required.
	server := grpc.NewServer()
	recorder := &invokeUsageRPCRecorder{}
	v1beta.RegisterTelemetryServiceServer(server, recorder)
	azdext.RegisterProjectServiceServer(server, &helpersProjectServer{
		err: status.Error(codes.NotFound, "project unavailable in test"),
	})
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
	root.SetArgs([]string{"invoke", "worker", "private prompt", "--protocol", "invocations", "--no-prompt"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	// Project lookup fails after the invoke mode is chosen; telemetry is still sent over gRPC.
	require.Error(t, root.ExecuteContext(t.Context()))

	requests := recorder.snapshot()
	require.Len(t, requests, 1)
	require.Equal(t, "agent.invoked", requests[0].GetEventName())
	require.Equal(t, map[string]string{
		"protocol": "invocations", "long_running": "false", "no_wait": "false",
	}, requests[0].GetAttributes())
}

func TestAgentLongRunningRequestOverGRPC(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{
		"kind": "hosted", "name": "worker",
		"protocols": []any{map[string]any{"protocol": "responses", "version": "1.0.0"}},
	})
	require.NoError(t, err)
	projectServer := &invokeUsageProjectThenFail{project: &azdext.ProjectConfig{
		Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
			"worker": {Name: "worker", Host: AiAgentHost, AdditionalProperties: props},
		},
	}}
	server := grpc.NewServer()
	recorder := &invokeUsageRPCRecorder{}
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
	root.SetArgs([]string{
		"invoke", "worker", "private prompt", "--protocol", "responses", "--long-running", "--no-wait", "--no-prompt",
	})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	require.ErrorContains(t, root.ExecuteContext(t.Context()), "project unavailable after protocol selection")

	requests := recorder.snapshot()
	require.Len(t, requests, 2) // root context and exactly one invoke event
	require.Equal(t, "agent.context.resolved", requests[0].GetEventName())
	require.Equal(t, "agent.invoked", requests[1].GetEventName())
	require.Equal(t, map[string]string{
		"protocol": "responses", "long_running": "true", "no_wait": "true",
	}, requests[1].GetAttributes())
}
