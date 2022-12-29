package telemetry

import (
	"context"
	"sync"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry/baggage"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/atomic"
)

// SetBaggageInContext sets the given attributes as baggage.
// Baggage attributes are set for the current running span, and for any child spans created.
func SetBaggageInContext(ctx context.Context, attributes ...attribute.KeyValue) context.Context {
	SetAttributesInContext(ctx, attributes...)
	return baggage.ContextWithAttributes(ctx, attributes)
}

// SetAttributesInContext sets the given attributes for the current running span.
func SetAttributesInContext(ctx context.Context, attributes ...attribute.KeyValue) {
	runningSpan := trace.SpanFromContext(ctx)
	runningSpan.SetAttributes(attributes...)
}

// Attributes that are global and set on all events
var global atomic.Value

// Attributes that are only set on command-level usage events
var usage atomic.Value

// mutex for multiple writers
var globalMu sync.Mutex
var usageMu sync.Mutex

func init() {
	global.Store(baggage.NewBaggage())
	usage.Store(baggage.NewBaggage())
}

// Sets global attributes that are included with all telemetry events emitted.
// If the attribute already exists, the value is replaced.
func SetGlobalAttributes(attributes ...attribute.KeyValue) {
	globalMu.Lock()
	defer globalMu.Unlock()

	baggage := global.Load().(baggage.Baggage)
	newBaggage := baggage.Set(attributes...)

	global.Store(newBaggage)
}

// Returns all global attributes set.
func GetGlobalAttributes() []attribute.KeyValue {
	baggage := global.Load().(baggage.Baggage)
	return baggage.Attributes()
}

// Sets usage attributes that are included with usage events emitted.
// If the attribute already exists, the value is replaced.
func SetUsageAttributes(attributes ...attribute.KeyValue) {
	usageMu.Lock()
	defer usageMu.Unlock()

	baggage := usage.Load().(baggage.Baggage)
	newBaggage := baggage.Set(attributes...)

	usage.Store(newBaggage)
}

// Sets or appends a value to a slice-type usage attribute that possibly exists.
// The attribute is expected to be a slice-type value, and matches the existing type.
// Otherwise, a strict replacement is performed.
func AppendUsageAttribute(attr attribute.KeyValue) {
	usageMu.Lock()
	defer usageMu.Unlock()

	baggage := usage.Load().(baggage.Baggage)
	val, ok := baggage.Lookup(attr.Key)
	if ok && val.Type() == attr.Value.Type() {
		switch attr.Value.Type() {
		case attribute.BOOLSLICE:
			attr = attr.Key.BoolSlice(append(val.AsBoolSlice(), attr.Value.AsBoolSlice()...))
		case attribute.INT64SLICE:
			attr = attr.Key.Int64Slice(append(val.AsInt64Slice(), attr.Value.AsInt64Slice()...))
		case attribute.FLOAT64SLICE:
			attr = attr.Key.Float64Slice(append(val.AsFloat64Slice(), attr.Value.AsFloat64Slice()...))
		case attribute.STRINGSLICE:
			attr = attr.Key.StringSlice(append(val.AsStringSlice(), attr.Value.AsStringSlice()...))
		}
	}

	newBaggage := baggage.Set(attr)
	usage.Store(newBaggage)
}

// Returns all usage attributes set.
func GetUsageAttributes() []attribute.KeyValue {
	baggage := usage.Load().(baggage.Baggage)
	return baggage.Attributes()
}
