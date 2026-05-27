// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tracing

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/tracing/baggage"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestSetAndGetAttributes(t *testing.T) {
	val := &valSynced{}
	val.val.Store(baggage.NewBaggage())
	setAttributes := []attribute.KeyValue{
		attribute.String("str", "val1"),
		attribute.Bool("bool", true),
		attribute.Int64("int64", 10),
		attribute.Float64("float64", 10.0),

		attribute.StringSlice("stringslice", []string{"v1", "v2"}),
		attribute.BoolSlice("boolslice", []bool{false, true}),
		attribute.Int64Slice("int64slice", []int64{1, 2}),
		attribute.Float64Slice("float64slice", []float64{1.0, 2.0}),
	}
	set(val, setAttributes)

	actual := get(val)
	assert.ElementsMatch(t, actual, setAttributes)
}

func TestAppendAttribute(t *testing.T) {
	tests := []struct {
		name     string
		existing []attribute.KeyValue
		set      attribute.KeyValue
		expected []attribute.KeyValue
	}{
		{"Set", []attribute.KeyValue{}, attribute.String("k", "v"), []attribute.KeyValue{attribute.String("k", "v")}},
		{"SetSlice",
			[]attribute.KeyValue{},
			attribute.StringSlice("k", []string{"v"}),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v"})}},

		{"ReplaceWhenUnmatched",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1"})},
			attribute.String("k", "v"),
			[]attribute.KeyValue{attribute.String("k", "v")}},

		{"AppendStringSlice",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1"})},
			attribute.StringSlice("k", []string{"v2"}),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})}},

		{"AppendBoolSlice",
			[]attribute.KeyValue{attribute.BoolSlice("k", []bool{true})},
			attribute.BoolSlice("k", []bool{false}),
			[]attribute.KeyValue{attribute.BoolSlice("k", []bool{true, false})}},

		{"AppendInt64Slice",
			[]attribute.KeyValue{attribute.Int64Slice("k", []int64{1})},
			attribute.Int64Slice("k", []int64{2}),
			[]attribute.KeyValue{attribute.Int64Slice("k", []int64{1, 2})}},

		{"AppendFloat64Slice",
			[]attribute.KeyValue{attribute.Float64Slice("k", []float64{1.0})},
			attribute.Float64Slice("k", []float64{2.0}),
			[]attribute.KeyValue{attribute.Float64Slice("k", []float64{1.0, 2.0})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := &valSynced{}
			val.val.Store(baggage.NewBaggage())

			set(val, tt.existing)

			appendTo(val, tt.set)

			attributes := get(val)

			assert.ElementsMatch(t, attributes, tt.expected)
		})
	}
}

func TestAppendAttributeUnique(t *testing.T) {
	tests := []struct {
		name     string
		existing []attribute.KeyValue
		set      attribute.KeyValue
		expected []attribute.KeyValue
	}{
		{"SetString",
			[]attribute.KeyValue{},
			attribute.String("k", "v"),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v"})}},
		{"SetSlice",
			[]attribute.KeyValue{},
			attribute.StringSlice("k", []string{"v"}),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v"})}},

		{"ReplaceStringWhenUnmatched",
			[]attribute.KeyValue{attribute.BoolSlice("k", []bool{true})},
			attribute.String("k", "v"),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v"})}},

		{"ReplaceStringSliceWhenUnmatched",
			[]attribute.KeyValue{attribute.BoolSlice("k", []bool{true})},
			attribute.StringSlice("k", []string{"v"}),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v"})}},

		{"AppendStringSlice",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1"})},
			attribute.StringSlice("k", []string{"v2"}),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})}},

		{"MergeStringSlice",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})},
			attribute.StringSlice("k", []string{"v2", "v3"}),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2", "v3"})}},

		{"ExistingStringSlice",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})},
			attribute.StringSlice("k", []string{"v2"}),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})}},

		{"AppendString",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1"})},
			attribute.String("k", "v2"),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})}},

		{"MergeString",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})},
			attribute.String("k", "v3"),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2", "v3"})}},

		{"ExistingString",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})},
			attribute.String("k", "v2"),
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1", "v2"})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := &valSynced{}
			val.val.Store(baggage.NewBaggage())

			set(val, tt.existing)

			appendToUnique(val, tt.set)

			attributes := get(val)

			assert.ElementsMatch(t, attributes, tt.expected)
		})
	}
}

func TestIncrementAttribute(t *testing.T) {
	tests := []struct {
		name     string
		existing []attribute.KeyValue
		set      attribute.KeyValue
		expected []attribute.KeyValue
	}{
		{"SetUnknown", []attribute.KeyValue{}, attribute.String("k", "v"), []attribute.KeyValue{attribute.String("k", "v")}},
		{"Set",
			[]attribute.KeyValue{},
			attribute.Float64("k", 5.0),
			[]attribute.KeyValue{attribute.Float64("k", 5.0)}},
		{"ReplaceWhenUnmatched",
			[]attribute.KeyValue{attribute.StringSlice("k", []string{"v1"})},
			attribute.Float64("k", 5.0),
			[]attribute.KeyValue{attribute.Float64("k", 5.0)}},
		{"IncrementFloat64",
			[]attribute.KeyValue{attribute.Float64("k", 5.0)},
			attribute.Float64("k", 5.0),
			[]attribute.KeyValue{attribute.Float64("k", 10.0)}},
		{"IncrementInt64",
			[]attribute.KeyValue{attribute.Int64("k", 5)},
			attribute.Int64("k", 5),
			[]attribute.KeyValue{attribute.Int64("k", 10)}},
		{"ConcatenateString",
			[]attribute.KeyValue{attribute.String("k", "v")},
			attribute.String("k", "v"),
			[]attribute.KeyValue{attribute.String("k", "vv")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val := &valSynced{}
			val.val.Store(baggage.NewBaggage())

			set(val, tt.existing)

			increment(val, tt.set)

			attributes := get(val)

			assert.ElementsMatch(t, attributes, tt.expected)
		})
	}
}

// resetGlobalAttributesForTest clears global attributes. Tests that mutate
// process-global attribute state should call this to avoid cross-test
// pollution.
func resetGlobalAttributesForTest() {
	globalVal.val.Store(baggage.NewBaggage())
}

// TestPublicAttributeHelpers exercises the public Set/Get/Append/Increment
// wrappers on the package-level globalVal and usageVal stores. These
// wrappers are thin delegates to the internal set/get/appendTo/increment
// helpers and are normally only invoked from cross-package callers; this
// test asserts the wiring directly so the package-local coverage report
// reflects them.
//
// The test mutates process-global state and must not run with t.Parallel().
func TestPublicAttributeHelpers(t *testing.T) {
	t.Run("UsageAttributes_SetGetReset", func(t *testing.T) {
		ResetUsageAttributesForTest()
		t.Cleanup(ResetUsageAttributesForTest)

		SetUsageAttributes(
			attribute.String("usage.str", "hello"),
			attribute.Bool("usage.bool", true),
			attribute.Int64("usage.int64", 42),
		)
		got := GetUsageAttributes()
		assert.ElementsMatch(t, got, []attribute.KeyValue{
			attribute.String("usage.str", "hello"),
			attribute.Bool("usage.bool", true),
			attribute.Int64("usage.int64", 42),
		})

		ResetUsageAttributesForTest()
		assert.Empty(t, GetUsageAttributes(),
			"ResetUsageAttributesForTest should clear all usage attributes")
	})

	t.Run("AppendUsageAttribute", func(t *testing.T) {
		ResetUsageAttributesForTest()
		t.Cleanup(ResetUsageAttributesForTest)

		AppendUsageAttribute(attribute.StringSlice("usage.list", []string{"a"}))
		AppendUsageAttribute(attribute.StringSlice("usage.list", []string{"b"}))

		got := GetUsageAttributes()
		assert.Len(t, got, 1)
		assert.Equal(t, []string{"a", "b"}, got[0].Value.AsStringSlice())
	})

	t.Run("AppendUsageAttributeUnique", func(t *testing.T) {
		ResetUsageAttributesForTest()
		t.Cleanup(ResetUsageAttributesForTest)

		AppendUsageAttributeUnique(attribute.StringSlice("usage.set", []string{"x"}))
		AppendUsageAttributeUnique(attribute.StringSlice("usage.set", []string{"x", "y"}))

		got := GetUsageAttributes()
		assert.Len(t, got, 1)
		assert.ElementsMatch(t, []string{"x", "y"}, got[0].Value.AsStringSlice())
	})

	t.Run("IncrementUsageAttribute", func(t *testing.T) {
		ResetUsageAttributesForTest()
		t.Cleanup(ResetUsageAttributesForTest)

		IncrementUsageAttribute(attribute.Int64("usage.count", 3))
		IncrementUsageAttribute(attribute.Int64("usage.count", 4))

		got := GetUsageAttributes()
		assert.Len(t, got, 1)
		assert.Equal(t, int64(7), got[0].Value.AsInt64())
	})

	t.Run("GlobalAttributes_SetGet", func(t *testing.T) {
		resetGlobalAttributesForTest()
		t.Cleanup(resetGlobalAttributesForTest)

		SetGlobalAttributes(
			attribute.String("global.k", "v"),
			attribute.Int64("global.n", 1),
		)
		got := GetGlobalAttributes()
		assert.ElementsMatch(t, got, []attribute.KeyValue{
			attribute.String("global.k", "v"),
			attribute.Int64("global.n", 1),
		})
	})
}

// TestSetBaggageInContext verifies the public context-baggage wrappers do
// not panic when invoked with a background context (no active span) and
// that SetBaggageInContext returns a derived context carrying the supplied
// attributes via the package-internal baggage helper.
func TestSetBaggageInContext(t *testing.T) {
	ctx := context.Background()
	attrs := []attribute.KeyValue{
		attribute.String("ctx.k", "v"),
		attribute.Int64("ctx.n", 7),
	}

	// SetAttributesInContext is a no-op when no span is attached; it
	// should run cleanly without panicking.
	assert.NotPanics(t, func() {
		SetAttributesInContext(ctx, attrs...)
	})

	// SetBaggageInContext must return a context that carries the
	// supplied attributes as baggage.
	out := SetBaggageInContext(ctx, attrs...)
	got := baggage.BaggageFromContext(out).Attributes()
	assert.ElementsMatch(t, got, attrs)
}
