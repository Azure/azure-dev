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
var globalVal = valSynced{}

// Attributes that are only set on command-level usage events
var usageVal = valSynced{}

// Atomic value with mutex for multiple writers
type valSynced struct {
	val atomic.Value
	mu  sync.Mutex
}

func init() {
	globalVal.val.Store(baggage.NewBaggage())
	usageVal.val.Store(baggage.NewBaggage())
}

func set(v *valSynced, attributes []attribute.KeyValue) {
	v.mu.Lock()
	defer v.mu.Unlock()

	baggage := v.val.Load().(baggage.Baggage)
	newBaggage := baggage.Set(attributes...)

	v.val.Store(newBaggage)
}

func get(v *valSynced) []attribute.KeyValue {
	baggage := v.val.Load().(baggage.Baggage)
	return baggage.Attributes()
}

func appendTo(v *valSynced, attr attribute.KeyValue) {
	v.mu.Lock()
	defer v.mu.Unlock()

	baggage := v.val.Load().(baggage.Baggage)
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
	v.val.Store(newBaggage)
}

// Sets global attributes that are included with all telemetry events emitted.
// If the attribute already exists, the value is replaced.
func SetGlobalAttributes(attributes ...attribute.KeyValue) {
	set(&globalVal, attributes)
}

// Returns all global attributes set.
func GetGlobalAttributes() []attribute.KeyValue {
	return get(&globalVal)
}

// Sets usage attributes that are included with usage events emitted.
// If the attribute already exists, the value is replaced.
func SetUsageAttributes(attributes ...attribute.KeyValue) {
	set(&usageVal, attributes)
}

// Returns all usage attributes set.
func GetUsageAttributes() []attribute.KeyValue {
	return get(&usageVal)
}

// Sets or appends a value to a slice-type usage attribute that possibly exists.
// The attribute is expected to be a slice-type value, and matches the existing type.
// Otherwise, a strict replacement is performed.
func AppendUsageAttribute(attr attribute.KeyValue) {
	appendTo(&usageVal, attr)
}
