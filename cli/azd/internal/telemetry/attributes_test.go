package telemetry

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/telemetry/baggage"
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
