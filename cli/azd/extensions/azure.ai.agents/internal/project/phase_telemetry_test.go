// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type phaseTelemetryServer struct {
	azdext.UnimplementedTelemetryServiceServer
	mu     sync.Mutex
	events []*azdext.ReportUsageRequest
	parent string
}

func (s *phaseTelemetryServer) ReportUsage(
	ctx context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, req)
	meta, _ := metadata.FromIncomingContext(ctx)
	s.parent = meta.Get("traceparent")[0]
	return &azdext.ReportUsageResponse{Accepted: true}, nil
}

func TestAgentProviderTimingsUseSharedUpAndStandaloneEntryPoints(t *testing.T) {
	for _, entry := range []string{"up", "standalone"} {
		t.Run(entry, func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			reporter := &phaseTelemetryServer{}
			server := grpc.NewServer()
			azdext.RegisterTelemetryServiceServer(server, reporter)
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
			config := inlineAgentService(t, map[string]any{
				"kind": "prompt", "name": "private-agent", "model": "gpt-4.1-mini", "instructions": "secret prompt",
			})
			provider := NewAgentServiceTargetProvider(client)
			progress := func(string) {}
			_, err = provider.Package(ctx, config, &azdext.ServiceContext{}, progress)
			require.NoError(t, err, "prompt packaging is an intentional no-op")
			_, err = provider.Publish(ctx, config, &azdext.ServiceContext{}, nil, nil, progress)
			require.NoError(t, err, "prompt publishing is an intentional no-op")
			_, err = provider.Deploy(ctx, config, &azdext.ServiceContext{}, nil, progress)
			require.Error(t, err, "the missing Project service must remain a deploy error")
			reporter.mu.Lock()
			defer reporter.mu.Unlock()
			names, attempts := map[string]string{}, map[string]bool{}
			for _, event := range reporter.events {
				if event.EventName == "agent.phase.completed" {
					require.Equal(t, "provider", event.Attributes["agent.phase.scope"])
					require.NotEmpty(t, event.Attributes["agent.phase.start_time"])
					require.NotEmpty(t, event.Attributes["agent.phase.end_time"])
					names[event.Attributes["agent.phase.name"]] = event.Attributes["agent.phase.outcome"]
					id := event.Attributes["agent.phase.attempt_id"]
					require.False(t, attempts[id])
					attempts[id] = true
				}
				for _, value := range event.Attributes {
					require.NotContains(t, value, "private-agent")
					require.NotContains(t, value, "secret prompt")
				}
			}
			require.Equal(t, map[string]string{"package": "success", "publish": "success", "deploy": "error"}, names)
			require.Equal(t, parent, reporter.parent)
		})
	}
}
