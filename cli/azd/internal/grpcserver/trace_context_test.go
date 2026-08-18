// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const (
	testTraceId = "68f1c4f4ef5346e69d7f196761d10c68"
	testSpanId  = "7fbdc197a52f4825"
)

const (
	otherTraceId = "3f1e2d4c5b6a79880123456789abcdef"
	otherSpanId  = "0123456789abcdef"
)

func testTraceparent() string {
	return "00-" + testTraceId + "-" + testSpanId + "-01"
}

func testClaims() *extensions.ExtensionClaims {
	return &extensions.ExtensionClaims{
		Traceparent: testTraceparent(),
		Tracestate:  "vendor=value",
	}
}

func contextWithClaims(ctx context.Context) context.Context {
	return extensions.WithClaimsContext(ctx, testClaims())
}

func Test_WithIncomingTraceContext_AcceptsChildTrace(t *testing.T) {
	t.Parallel()

	md := metadata.Pairs(
		traceparentHeader, "00-"+testTraceId+"-"+otherSpanId+"-01",
		tracestateHeader, "vendor=child",
	)
	ctx := contextWithClaims(metadata.NewIncomingContext(t.Context(), md))

	spanContext := trace.SpanContextFromContext(withIncomingTraceContext(ctx))

	require.True(t, spanContext.IsValid())
	require.Equal(t, testTraceId, spanContext.TraceID().String())
	require.Equal(t, otherSpanId, spanContext.SpanID().String())
	require.Equal(t, "vendor=child", spanContext.TraceState().String())
}

func Test_WithIncomingTraceContext_FallsBackToSignedTrace(t *testing.T) {
	t.Parallel()

	tests := map[string]metadata.MD{
		"no metadata":        nil,
		"no traceparent":     metadata.Pairs("authorization", "token"),
		"empty traceparent":  metadata.Pairs(traceparentHeader, ""),
		"malformed":          metadata.Pairs(traceparentHeader, "not-a-traceparent"),
		"unsupported format": metadata.Pairs(traceparentHeader, "99-"+testTraceId),
		"different trace": metadata.Pairs(
			traceparentHeader,
			"00-"+otherTraceId+"-"+otherSpanId+"-01",
		),
	}

	for name, md := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if md != nil {
				ctx = metadata.NewIncomingContext(ctx, md)
			}
			ctx = contextWithClaims(ctx)

			spanContext := trace.SpanContextFromContext(withIncomingTraceContext(ctx))
			require.True(t, spanContext.IsValid())
			require.Equal(t, testTraceId, spanContext.TraceID().String())
			require.Equal(t, testSpanId, spanContext.SpanID().String())
		})
	}
}

func Test_WithIncomingTraceContext_RejectsCallerWithoutSignedTrace(t *testing.T) {
	t.Parallel()

	ctx := metadata.NewIncomingContext(t.Context(), metadata.Pairs(
		traceparentHeader, testTraceparent(),
	))
	spanContext := trace.SpanContextFromContext(withIncomingTraceContext(ctx))
	require.False(t, spanContext.IsValid())
}

func Test_TraceContextInterceptorsUseSignedTrace(t *testing.T) {
	t.Parallel()

	callerParent := "00-" + otherTraceId + "-" + otherSpanId + "-01"
	baseContext := contextWithClaims(metadata.NewIncomingContext(
		t.Context(), metadata.Pairs(traceparentHeader, callerParent)))
	server := &Server{}

	t.Run("unary", func(t *testing.T) {
		var got trace.SpanContext
		_, err := server.traceContextInterceptor()(
			baseContext,
			nil,
			&grpc.UnaryServerInfo{},
			func(ctx context.Context, req any) (any, error) {
				got = trace.SpanContextFromContext(ctx)
				return nil, nil
			},
		)
		require.NoError(t, err)
		require.Equal(t, testTraceId, got.TraceID().String())
	})

	t.Run("stream", func(t *testing.T) {
		var got trace.SpanContext
		stream := &traceTestServerStream{ctx: baseContext}
		err := server.traceContextStreamInterceptor()(
			nil,
			stream,
			&grpc.StreamServerInfo{},
			func(srv any, stream grpc.ServerStream) error {
				got = trace.SpanContextFromContext(stream.Context())
				return nil
			},
		)
		require.NoError(t, err)
		require.Equal(t, testTraceId, got.TraceID().String())
	})
}

type traceTestServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *traceTestServerStream) Context() context.Context {
	return s.ctx
}
