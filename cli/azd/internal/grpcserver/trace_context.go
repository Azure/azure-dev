// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// W3C trace context metadata keys sent by the extension SDK.
const (
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
)

// traceContextInterceptor restores the authenticated caller's trace
// context so host spans join the command's trace.
func (s *Server) traceContextInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		return handler(withIncomingTraceContext(ctx), req)
	}
}

// traceContextStreamInterceptor is the streaming counterpart of
// traceContextInterceptor.
func (s *Server) traceContextStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		return handler(srv, &contextStream{
			ServerStream: ss,
			ctx:          withIncomingTraceContext(ss.Context()),
		})
	}
}

// withIncomingTraceContext accepts only the signed trace.
// It otherwise uses the signed context and ignores caller metadata.
func withIncomingTraceContext(ctx context.Context) context.Context {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil || claims.Traceparent == "" {
		return ctx
	}

	tokenCarrier := propagation.MapCarrier{
		traceparentHeader: claims.Traceparent,
	}
	if claims.Tracestate != "" {
		tokenCarrier[tracestateHeader] = claims.Tracestate
	}

	tokenContext := propagation.TraceContext{}.Extract(context.Background(), tokenCarrier)
	tokenSpanContext := trace.SpanContextFromContext(tokenContext)
	if !tokenSpanContext.IsValid() {
		return ctx
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return propagation.TraceContext{}.Extract(ctx, tokenCarrier)
	}

	parent := md.Get(traceparentHeader)
	if len(parent) == 0 || parent[0] == "" {
		return propagation.TraceContext{}.Extract(ctx, tokenCarrier)
	}

	carrier := propagation.MapCarrier{traceparentHeader: parent[0]}
	if state := md.Get(tracestateHeader); len(state) > 0 {
		carrier[tracestateHeader] = state[0]
	}

	incomingContext := propagation.TraceContext{}.Extract(context.Background(), carrier)
	incomingSpanContext := trace.SpanContextFromContext(incomingContext)
	if incomingSpanContext.IsValid() &&
		incomingSpanContext.TraceID() == tokenSpanContext.TraceID() {
		return propagation.TraceContext{}.Extract(ctx, carrier)
	}

	return propagation.TraceContext{}.Extract(ctx, tokenCarrier)
}
