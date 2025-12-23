// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestNewContext_FromEnvironment(t *testing.T) {
	traceparent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tracestate := "rojo=00f067aa0ba902b7"

	t.Setenv(TraceparentEnv, traceparent)
	t.Setenv(TracestateEnv, tracestate)

	ctx := NewContext()
	sc := trace.SpanContextFromContext(ctx)

	require.True(t, sc.IsValid(), "span context should be extracted from environment")
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)
	require.Equal(t, traceID, sc.TraceID())
	require.Equal(t, spanID, sc.SpanID())
	require.Equal(t, tracestate, sc.TraceState().String())
}

func TestNewContext_NoEnvironment(t *testing.T) {
	t.Setenv(TraceparentEnv, "")
	t.Setenv(TracestateEnv, "")

	ctx := NewContext()
	sc := trace.SpanContextFromContext(ctx)

	require.False(t, sc.IsValid(), "span context should be absent when no environment is set")
}
