// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package baggage provides an implementation of storing trace-level context data, i.e. baggage.
// Unlike OpenTelemetry's implementation of baggage, this baggage is only propagated to child spans of the current local
// process.
// Information is not propagated with any external calls.
package baggage

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
)

type contextKey string

const baggageKey contextKey = "baggageState"

// ContextWithAttributes returns a copy of ctx with attributes set.
func ContextWithAttributes(ctx context.Context, attributes []attribute.KeyValue) context.Context {
	bg := BaggageFromContext(ctx)
	bg = bg.Set(attributes...)
	return ContextWithBaggage(ctx, bg)
}

// ContextWithBaggage returns a copy of ctx with baggage.
func ContextWithBaggage(ctx context.Context, tv Baggage) context.Context {
	return context.WithValue(ctx, baggageKey, tv.m)
}

// ContextWithoutBaggage returns a copy of ctx with no baggage.
func ContextWithoutBaggage(ctx context.Context) context.Context {
	return context.WithValue(ctx, baggageKey, NewBaggage())
}

// FromContext returns the baggage contained in ctx.
func BaggageFromContext(ctx context.Context) Baggage {
	state, ok := ctx.Value(baggageKey).(map[attribute.Key]attribute.Value)
	if !ok || state == nil {
		return NewBaggage()
	}

	return Baggage{m: state}
}
