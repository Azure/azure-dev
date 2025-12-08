// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/metadata"

	"github.com/stretchr/testify/require"
)

func TestWithTracing_NoSpanContext(t *testing.T) {
	ctx := context.Background()

	newCtx := WithTracing(ctx)

	_, ok := metadata.FromOutgoingContext(newCtx)
	require.False(t, ok, "metadata should be untouched when no span context exists")
}

func TestWithTracing_AppendsTraceparentAndTracestate(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)
	traceState, err := trace.ParseTraceState("foo=bar")
	require.NoError(t, err)

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		TraceState: traceState,
	})

	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	newCtx := WithTracing(ctx)

	md, ok := metadata.FromOutgoingContext(newCtx)
	require.True(t, ok, "metadata should be populated when span context exists")
	values := md.Get(TraceparentKey)
	require.Len(t, values, 1)
	require.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", values[0])

	stateValues := md.Get(TracestateKey)
	require.Len(t, stateValues, 1)
	require.Equal(t, "foo=bar", stateValues[0])
}

func TestWithTracing_AppendsTraceparentOnly(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		// No TraceState set
	})

	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	newCtx := WithTracing(ctx)

	md, ok := metadata.FromOutgoingContext(newCtx)
	require.True(t, ok, "metadata should be populated when span context exists")
	values := md.Get(TraceparentKey)
	require.Len(t, values, 1)
	require.Equal(t, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", values[0])

	stateValues := md.Get(TracestateKey)
	require.Empty(t, stateValues, "tracestate should not be present when empty")
}
