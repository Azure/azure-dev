// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	"go.opentelemetry.io/otel/propagation"
)

const traceparentKey = "traceparent"

// GetTraceParentFromContext extracts the current span context and formats it as a W3C traceparent string.
// Returns empty string if no valid span context is found.
func GetTraceParentFromContext(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier.Get(traceparentKey)
}
