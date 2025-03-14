// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tracing

import (
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
