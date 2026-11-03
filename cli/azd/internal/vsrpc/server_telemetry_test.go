// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.lsp.dev/jsonrpc2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

var rpcSpanRecorder = tracetest.NewSpanRecorder()

func TestMain(m *testing.M) {
	// Install before server tests initialize telemetry: tracing caches the first provider.
	otel.SetTracerProvider(tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(rpcSpanRecorder)))
	os.Exit(m.Run())
}

func TestServeRpcExcludesInvocationDropAggregates(t *testing.T) {
	tracing.ResetUsageAttributesForTest()
	t.Cleanup(tracing.ResetUsageAttributesForTest)
	baseline := len(rpcSpanRecorder.Ended())
	spans := func() []tracesdk.ReadOnlySpan { return rpcSpanRecorder.Ended()[baseline:] }

	tracing.SetUsageAttributes(fields.ExtensionVersion.String("1.0.0"))
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	handler := func(ctx context.Context, _ jsonrpc2.Conn, reply jsonrpc2.Replier, req jsonrpc2.Request) error {
		if req.Method() == "concurrent" {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if req.Method() != "later" {
			tracing.AppendUsageAttributeUnique(fields.ExtensionUsageDropped.String("unattributed@source_ineligible"))
			tracing.IncrementUsageAttribute(fields.ExtensionUsageDroppedCount.Int64(1))
		}
		return reply(ctx, true, nil)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveRpc(w, r, map[string]Handler{"drop": handler, "later": handler, "concurrent": handler})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ws, _, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	conn := jsonrpc2.NewConn(newWebSocketStream(ws))
	defer conn.Close()
	conn.Go(ctx, nil)

	for _, method := range []string{"drop", "later", "later"} {
		_, err := conn.Call(ctx, method, nil, nil)
		require.NoError(t, err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := conn.Call(ctx, "concurrent", nil, nil)
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(release)
	for range 2 {
		require.NoError(t, <-results)
	}

	// Responses can arrive before the deferred span finalizer completes.
	require.Eventually(t, func() bool { return len(spans()) == 5 }, 5*time.Second, 10*time.Millisecond)
	for _, span := range spans() {
		attrs := map[attribute.Key]attribute.Value{}
		for _, attr := range span.Attributes() {
			attrs[attr.Key] = attr.Value
		}
		require.NotContains(t, attrs, fields.ExtensionUsageDropped.Key)
		require.NotContains(t, attrs, fields.ExtensionUsageDroppedCount.Key)
		require.Equal(t, "1.0.0", attrs[fields.ExtensionVersion.Key].AsString())
	}
	// Filtering must not consume or mutate the hosting command's aggregate.
	_, command := tracing.Start(ctx, "cmd.vs-server")
	command.SetAttributes(tracing.GetUsageAttributes()...)
	command.End()
	attrs := map[attribute.Key]attribute.Value{}
	for _, attr := range spans()[5].Attributes() {
		attrs[attr.Key] = attr.Value
	}
	require.Equal(t, int64(3), attrs[fields.ExtensionUsageDroppedCount.Key].AsInt64())
	require.Equal(t, []string{"unattributed@source_ineligible"}, attrs[fields.ExtensionUsageDropped.Key].AsStringSlice())
}
