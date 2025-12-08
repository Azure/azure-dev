// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc/metadata"
)

const (
	TraceparentKey = "traceparent"
	TracestateKey  = "tracestate"

	// https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/context/env-carriers.md

	TraceparentEnv = "TRACEPARENT"
	TracestateEnv  = "TRACESTATE"
)

// WithTracing appends the W3C traceparent for the current span to outgoing gRPC metadata.
// If no span context is present, the original context is returned unchanged.
func WithTracing(ctx context.Context) context.Context {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	traceparent := carrier.Get(TraceparentKey)
	if traceparent == "" {
		return ctx
	}

	tracestate := carrier.Get(TracestateKey)
	if tracestate != "" {
		return metadata.AppendToOutgoingContext(ctx, TraceparentKey, traceparent, TracestateKey, tracestate)
	}

	return metadata.AppendToOutgoingContext(ctx, TraceparentKey, traceparent)
}
