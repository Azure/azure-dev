// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type provisionTimingServer struct {
	azdext.UnimplementedTelemetryServiceServer
	azdext.UnimplementedEnvironmentServiceServer
	mu     sync.Mutex
	events []*azdext.ReportUsageRequest
	code   codes.Code
	parent string
}

func (s *provisionTimingServer) ReportUsage(
	ctx context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, req)
	md, _ := metadata.FromIncomingContext(ctx)
	s.parent = md.Get("traceparent")[0]
	return &azdext.ReportUsageResponse{Accepted: true}, nil
}

func (s *provisionTimingServer) GetValue(
	context.Context, *azdext.GetEnvRequest,
) (*azdext.KeyValueResponse, error) {
	if s.code != codes.OK {
		return nil, status.Error(s.code, "resource response not recorded")
	}
	return &azdext.KeyValueResponse{}, nil
}

func (s *provisionTimingServer) GetValues(
	context.Context, *azdext.GetEnvironmentRequest,
) (*azdext.KeyValueListResponse, error) {
	if s.code != codes.OK {
		return nil, status.Error(s.code, "resource response not recorded")
	}
	return &azdext.KeyValueListResponse{}, nil
}

func TestProvisionProviderTimingsCoverSharedEntriesAndEarlyReturns(t *testing.T) {
	for _, entry := range []string{"up", "provision"} {
		for _, test := range []struct {
			name    string
			reuse   bool
			code    codes.Code
			outcome string
		}{
			{"reuse", true, codes.OK, "success"},
			{"template error", false, codes.OK, "error"},
			{"canceled", true, codes.Canceled, "canceled"},
			{"timeout", true, codes.DeadlineExceeded, "timeout"},
		} {
			t.Run(entry+"/"+test.name, func(t *testing.T) {
				capture := &provisionTimingServer{code: test.code}
				server := grpc.NewServer()
				azdext.RegisterTelemetryServiceServer(server, capture)
				azdext.RegisterEnvironmentServiceServer(server, capture)
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				require.NoError(t, err)
				done := make(chan error, 1)
				go func() { done <- server.Serve(listener) }()
				t.Cleanup(func() { server.Stop(); require.NoError(t, <-done) })
				client, err := azdext.NewAzdClient(azdext.WithAddress(listener.Addr().String()))
				require.NoError(t, err)
				t.Cleanup(client.Close)
				const parent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
				ctx := propagation.TraceContext{}.Extract(t.Context(), propagation.MapCarrier{"traceparent": parent})
				ctx = azdext.WithAccessToken(ctx, "test-token")
				provider := &FoundryProvisioningProvider{
					azdClient: client, projectPath: t.TempDir(), existingProjectReuseOnly: test.reuse,
				}
				result, err := provider.Deploy(ctx, func(string) {})
				if test.outcome == "success" {
					require.NoError(t, err)
					require.NotNil(t, result)
				} else {
					require.Error(t, err)
				}
				capture.mu.Lock()
				defer capture.mu.Unlock()
				require.NotEmpty(t, capture.events)
				first := capture.events[0]
				require.Equal(t, "agent.phase.completed", first.EventName)
				require.Equal(t, "provision", first.Attributes["agent.phase.name"])
				require.Equal(t, "provider", first.Attributes["agent.phase.scope"])
				require.Equal(t, test.outcome, first.Attributes["agent.phase.outcome"])
				require.Equal(t, parent, capture.parent)
				require.NotEmpty(t, first.Attributes["agent.phase.start_time"])
				require.NotEmpty(t, first.Attributes["agent.phase.end_time"])
				for _, event := range capture.events[1:] {
					require.Equal(t, first.Attributes["agent.phase.attempt_id"], event.Attributes["agent.phase.attempt_id"])
				}
			})
		}
	}
}
