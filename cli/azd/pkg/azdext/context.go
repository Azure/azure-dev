// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/propagation"
)

// NewContext initializes a new context with tracing information extracted from environment variables.
func NewContext() context.Context {
	ctx := context.Background()
	parent := os.Getenv(TraceparentEnv)
	state := os.Getenv(TracestateEnv)

	if parent != "" {
		tc := propagation.TraceContext{}
		return tc.Extract(ctx, propagation.MapCarrier{
			TraceparentKey: parent,
			TracestateKey:  state,
		})
	}

	return ctx
}
