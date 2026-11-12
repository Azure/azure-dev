// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	foundryTelemetry "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type inspectorUsageServer struct {
	azdext.UnimplementedTelemetryServiceServer
	requests chan *azdext.ReportUsageRequest
}

func (s *inspectorUsageServer) ReportUsage(
	_ context.Context, req *azdext.ReportUsageRequest,
) (*azdext.ReportUsageResponse, error) {
	s.requests <- req
	return &azdext.ReportUsageResponse{Accepted: false}, nil
}

func TestNewUsageReporterUsesStableRPC(t *testing.T) {
	recorder := &inspectorUsageServer{requests: make(chan *azdext.ReportUsageRequest, 1)}
	server := grpc.NewServer()
	require.Equal(t, "azd.extensions.v1.TelemetryService", azdext.TelemetryService_ServiceDesc.ServiceName)
	azdext.RegisterTelemetryServiceServer(server, recorder)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	t.Setenv("AZD_SERVER", listener.Addr().String())

	report, closeClient := newUsageReporter(t.Context())
	t.Cleanup(closeClient)
	require.NotNil(t, report)
	report(foundryTelemetry.Event{Name: "inspector.funnel.stage", Attributes: map[string]string{
		"stage": "ui_ready", "outcome": "succeeded",
	}})
	select {
	case req := <-recorder.requests:
		require.Equal(t, "inspector.funnel.stage", req.GetEventName())
		require.Equal(t, map[string]string{"stage": "ui_ready", "outcome": "succeeded"}, req.GetAttributes())
	default:
		t.Fatal("usage event did not reach the stable telemetry service")
	}
}

func TestUsageReporterForwardsEventWithCommandContext(t *testing.T) {
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(
		"authorization", "token",
		"traceparent", "00-11111111111111111111111111111111-2222222222222222-01",
	))
	reporter := &recordingUsageReporter{}

	usageReporter(ctx, reporter)(foundryTelemetry.Event{Name: "inspector.funnel.stage", Attributes: map[string]string{
		"stage":   "ui_ready",
		"outcome": "succeeded",
	}})

	require.Equal(t, foundryTelemetry.Event{
		Name: "inspector.funnel.stage",
		Attributes: map[string]string{
			"stage":   "ui_ready",
			"outcome": "succeeded",
		},
	}, reporter.event)
	md, ok := metadata.FromOutgoingContext(reporter.ctx)
	require.True(t, ok)
	require.Equal(t, []string{"token"}, md.Get("authorization"))
	require.Equal(t, []string{
		"00-11111111111111111111111111111111-2222222222222222-01",
	}, md.Get("traceparent"))
}

func TestInjectSSEEventsSynthesizesEventLines(t *testing.T) {
	input := "data: {\"type\":\"response.output_text.delta\"}\n\n"

	output, err := io.ReadAll(injectSSEEvents(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("read injected stream: %v", err)
	}

	got := string(output)
	if !strings.Contains(got, "event: response.output_text.delta\n") {
		t.Fatalf("output should include synthesized event line, got %q", got)
	}
	if !strings.Contains(got, input) {
		t.Fatalf("output should preserve original data line, got %q", got)
	}
}

func TestInjectSSEEventsPropagatesReadError(t *testing.T) {
	wantErr := errors.New("read failed")

	_, err := io.ReadAll(injectSSEEvents(errorReader{err: wantErr}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("read error = %v, want %v", err, wantErr)
	}
}

func TestReadSSEStreamDrainsAfterCompleted(t *testing.T) {
	pr, pw := io.Pipe()
	writeDone := make(chan error, 1)
	readDone := make(chan error, 1)

	go func() {
		readDone <- readSSEStream(pr, "local")
	}()

	go func() {
		_, err := fmt.Fprint(
			pw,
			"event: response.output_text.delta\n",
			"data: {\"delta\":\"hello\"}\n",
			"\n",
			"event: response.completed\n",
			"data: {\"response\":{\"status\":\"completed\"}}\n",
			"\n",
			"event: response.output_text.delta\n",
			"data: {\"delta\":\"trailing\"}\n",
			"\n",
		)
		if err == nil {
			err = pw.Close()
		}
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write stream: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer blocked after response.completed")
	}

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("readSSEStream returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readSSEStream did not finish after source closed")
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}

type recordingUsageReporter struct {
	ctx   context.Context
	event foundryTelemetry.Event
}

func (r *recordingUsageReporter) Report(ctx context.Context, event foundryTelemetry.Event) {
	r.ctx = ctx
	r.event = event
}
