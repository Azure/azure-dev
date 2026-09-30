// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
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

func startInvokeUsageRPCServer(t *testing.T, project azdext.ProjectServiceServer) *invokeUsageRPCRecorder {
	t.Helper()
	server := grpc.NewServer()
	recorder := &invokeUsageRPCRecorder{}
	v1beta.RegisterTelemetryServiceServer(server, recorder)
	azdext.RegisterProjectServiceServer(server, project)
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
	return recorder
}

func TestAgentInvokeSelectedRequestOverGRPC(t *testing.T) {
	for _, tt := range []struct {
		name, protocol string
		longRunning    bool
		noWait         bool
	}{
		{"invocations", "invocations", false, false},
		{"long-running responses", "responses", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := startInvokeUsageRPCServer(t, &helpersProjectServer{})
			action := &InvokeAction{
				flags: &invokeFlags{
					protocol: tt.protocol, message: "private prompt", longRunning: tt.longRunning, noWait: tt.noWait,
				},
				endpoint:              &parsedAgentEndpoint{},
				resolvedRemoteContext: &remoteContext{name: "test"},
				credential:            invokeUsageFailingCredential{},
			}
			// After target resolution, authentication fails; telemetry still travels over gRPC.
			require.ErrorContains(t, action.Run(t.Context()), "failed to get auth token")

			requests := recorder.snapshot()
			require.Len(t, requests, 1)
			require.Equal(t, "agent.invoke.selected", requests[0].GetEventName())
			require.Equal(t, map[string]string{
				"agent.invoke.protocol":     tt.protocol,
				"agent.invoke.long_running": strconv.FormatBool(tt.longRunning),
				"agent.invoke.no_wait":      strconv.FormatBool(tt.noWait),
			}, requests[0].GetAttributes())
		})
	}
}

func TestNonHostedAgentInvokeDoesNotReportAgentInvokeSelected(t *testing.T) {
	for _, tt := range []struct{ kind, protocol string }{
		{"prompt", "responses"}, {"prompt", "invocations"}, {"prompt", "a2a"},
		{"voice", "invocations"}, {"workflow", "invocations"},
	} {
		t.Run(tt.kind+"/"+tt.protocol, func(t *testing.T) {
			props, err := structpb.NewStruct(map[string]any{
				"kind": tt.kind, "name": "assistant", "model": "gpt", "instructions": "help",
			})
			require.NoError(t, err)
			recorder := startInvokeUsageRPCServer(t, &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(), Services: map[string]*azdext.ServiceConfig{
					"assistant": {Name: "assistant", Host: AiAgentHost, AdditionalProperties: props},
				},
			}})

			root := NewRootCommand()
			root.SetArgs([]string{"invoke", "assistant", "hello", "--protocol", tt.protocol, "--no-prompt"})
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			// The fake host cannot supply prompt-agent environment settings.
			require.Error(t, root.ExecuteContext(t.Context()))

			requests := recorder.snapshot()
			require.Len(t, requests, 1)
			require.Equal(t, "agent.context.resolved", requests[0].GetEventName())
			require.Equal(t, tt.kind, requests[0].GetAttributes()["agent.kind"])
		})
	}
}
