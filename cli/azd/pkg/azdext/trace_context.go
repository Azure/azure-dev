// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
)

const traceparentKey = "traceparent"

// ContextFromTraceParent creates a new context with the span context extracted from the traceparent string.
func ContextFromTraceParent(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	carrier := propagation.MapCarrier{
		traceparentKey: traceparent,
	}
	return propagation.TraceContext{}.Extract(ctx, carrier)
}
