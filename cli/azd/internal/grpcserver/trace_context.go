// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// W3C trace context metadata keys sent by the extension SDK.
const (
	traceparentHeader = "traceparent"
	tracestateHeader  = "tracestate"
)

// traceContextInterceptor restores the caller's W3C trace context so spans the
// host records while serving an extension call join the command's trace
// instead of starting an unrelated root span.
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

// withIncomingTraceContext extracts W3C trace context from gRPC metadata.
// A missing or malformed traceparent leaves the context untouched, so calls
// from older extensions keep working.
func withIncomingTraceContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}

	parent := md.Get(traceparentHeader)
	if len(parent) == 0 || parent[0] == "" {
		return ctx
	}

	carrier := propagation.MapCarrier{traceparentHeader: parent[0]}
	if state := md.Get(tracestateHeader); len(state) > 0 {
		carrier[tracestateHeader] = state[0]
	}

	return propagation.TraceContext{}.Extract(ctx, carrier)
}
