// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tracing

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/propagation"
)

const (
	traceparentKey = "traceparent"
	tracestateKey  = "tracestate"

	// https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/context/env-carriers.md

	traceparentEnv = "TRACEPARENT"
	tracestateEnv  = "TRACESTATE"
)

// ContextFromEnv initializes the tracing context from environment variables.
func ContextFromEnv(ctx context.Context) context.Context {
	parent := os.Getenv(traceparentEnv)
	state := os.Getenv(tracestateEnv)

	if parent != "" {
		tc := propagation.TraceContext{}
		return tc.Extract(ctx, propagation.MapCarrier{
			traceparentKey: parent,
			tracestateKey:  state})
	}

	return ctx
}

// Environ returns environment variables for propagating tracing context.
//
// This can be used to set environment variables for child processes to
// continue the trace.
func Environ(ctx context.Context) []string {
	tm := propagation.MapCarrier{}
	tc := propagation.TraceContext{}
	tc.Inject(ctx, &tm)

	if parent := tm.Get(traceparentKey); parent != "" {
		environ := []string{
			traceparentEnv + "=" + parent,
		}

		if state := tm.Get(tracestateKey); state != "" {
			environ = append(environ, tracestateEnv+"="+state)
		}
		return environ
	}

	return nil
}
