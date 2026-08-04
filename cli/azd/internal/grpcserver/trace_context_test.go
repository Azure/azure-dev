// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"
)

const (
	testTraceId = "68f1c4f4ef5346e69d7f196761d10c68"
	testSpanId  = "7fbdc197a52f4825"
)

func testTraceparent() string {
	return "00-" + testTraceId + "-" + testSpanId + "-01"
}

func Test_WithIncomingTraceContext_RestoresCallerTrace(t *testing.T) {
	t.Parallel()

	md := metadata.Pairs(
		traceparentHeader, testTraceparent(),
		tracestateHeader, "vendor=value",
	)
	ctx := metadata.NewIncomingContext(t.Context(), md)

	spanContext := trace.SpanContextFromContext(withIncomingTraceContext(ctx))

	require.True(t, spanContext.IsValid())
	require.Equal(t, testTraceId, spanContext.TraceID().String())
	require.Equal(t, testSpanId, spanContext.SpanID().String())
	require.Equal(t, "vendor=value", spanContext.TraceState().String())
}

func Test_WithIncomingTraceContext_LeavesContextAlone(t *testing.T) {
	t.Parallel()

	tests := map[string]metadata.MD{
		"no metadata":        nil,
		"no traceparent":     metadata.Pairs("authorization", "token"),
		"empty traceparent":  metadata.Pairs(traceparentHeader, ""),
		"malformed":          metadata.Pairs(traceparentHeader, "not-a-traceparent"),
		"unsupported format": metadata.Pairs(traceparentHeader, "99-"+testTraceId),
	}

	for name, md := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if md != nil {
				ctx = metadata.NewIncomingContext(ctx, md)
			}

			require.False(t, trace.SpanContextFromContext(
				withIncomingTraceContext(ctx)).IsValid())
		})
	}
}
