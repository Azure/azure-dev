package appinsightsexporter

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/microsoft/ApplicationInsights-Go/appinsights/contracts"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.6.1"
	"go.opentelemetry.io/otel/trace"
)

func TestSpanToEnvelope(t *testing.T) {
	spanStub := getDefaultSpanStub()
	success := spanStub.Snapshot()

	spanStub.Status = tracesdk.Status{Code: codes.Error}
	unknownFailure := spanStub.Snapshot()

	spanStub.Status = tracesdk.Status{Code: codes.Error, Description: "PreconditionFailed"}
	preconditionFailure := spanStub.Snapshot()

	spanStub.Status = tracesdk.Status{Code: codes.Unset}
	unsetSuccess := spanStub.Snapshot()

	tests := []struct {
		name  string
		input tracesdk.ReadOnlySpan
	}{
		{"success", success},
		{"unknownFailure", unknownFailure},
		{"preconditionFailure", preconditionFailure},
		{"unsetSuccess", unsetSuccess},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			span := test.input
			envelope := SpanToEnvelope(span)

			assertEnvelopeFields(t, envelope, span)
			assertContextTags(t, envelope, span)

			data := envelope.Data.(*contracts.Data).BaseData.(*contracts.RequestData)
			assertRequestData(t, data, span)
		})
	}
}

func assertEnvelopeFields(t *testing.T, envelope *contracts.Envelope, span tracesdk.ReadOnlySpan) {
	assert.Equal(t, 1, envelope.Ver)
	assert.Equal(t, "Microsoft.ApplicationInsights.Request", envelope.Name)
	envelopeTime, err := time.Parse(time.RFC3339Nano, envelope.Time)
	if err != nil {
		assert.Fail(t, fmt.Sprintf("Could not parse time field: %s", err))
	}
	assert.Equal(t, span.StartTime(), envelopeTime)
	assert.Equal(t, 100.0, envelope.SampleRate)

	// These fields are not set until transmission
	assert.Equal(t, "", envelope.Seq)
	assert.Equal(t, "", envelope.IKey)
}

func assertContextTags(t *testing.T, envelope *contracts.Envelope, span tracesdk.ReadOnlySpan) {
	assert.Contains(t, envelope.Tags, contracts.OperationId)
	assert.Equal(t, span.SpanContext().TraceID().String(), envelope.Tags[contracts.OperationId])

	assert.Contains(t, envelope.Tags, contracts.OperationParentId)
	assert.Equal(t, span.Parent().SpanID().String(), envelope.Tags[contracts.OperationParentId])

	for _, resAttrib := range span.Resource().Attributes() {
		if isResourceContextTagField(string(resAttrib.Key)) {
			key := string(resAttrib.Key)

			assert.Contains(t, envelope.Tags, key)
			assert.Equal(t, resAttrib.Value.AsString(), envelope.Tags[key])
		}
	}

	for _, attrib := range span.Attributes() {
		if isAttributeContextTagField(string(attrib.Key)) {
			key := string(attrib.Key)

			assert.Contains(t, envelope.Tags, key)
			assert.Equal(t, attrib.Value.AsString(), envelope.Tags[key])
		}
	}
}

func assertRequestData(t *testing.T, data *contracts.RequestData, span tracesdk.ReadOnlySpan) {
	assert.Equal(t, 2, data.Ver)
	assert.Equal(t, span.SpanContext().SpanID().String(), data.Id)
	assert.Equal(t, span.Name(), data.Name)
	assert.Equal(t, formatDuration(span.EndTime().Sub(span.StartTime())), data.Duration)
	assert.Equal(t, "", data.Url)

	expectedLength := 0

	for _, resAttrib := range span.Resource().Attributes() {
		if !isResourceContextTagField(string(resAttrib.Key)) {
			expectedLength++
			assertAttributeInPropertiesOrMeasurement(t, resAttrib, data.Properties, data.Measurements)
		}
	}

	for _, attrib := range span.Attributes() {
		if !isAttributeContextTagField(string(attrib.Key)) {
			expectedLength++
			assertAttributeInPropertiesOrMeasurement(t, attrib, data.Properties, data.Measurements)
		}
	}

	assert.Equal(t, expectedLength, len(data.Properties)+len(data.Measurements))

	if span.Status().Code == codes.Error {
		assert.Equal(t, false, data.Success)
		if len(span.Status().Description) > 0 {
			assert.Equal(t, span.Status().Description, data.ResponseCode)
		} else {
			assert.Equal(t, "UnknownFailure", data.ResponseCode)
		}
	} else {
		assert.Equal(t, true, data.Success)
		assert.Equal(t, "Success", data.ResponseCode)
	}
}

func assertAttributeInPropertiesOrMeasurement(
	t *testing.T,
	attrib attribute.KeyValue,
	properties map[string]string,
	measurements map[string]float64,
) {
	switch attrib.Value.Type() {
	case attribute.BOOL, attribute.STRING:
		assert.Contains(t, properties, string(attrib.Key))
		assert.Equal(t, attrib.Value.Emit(), properties[string(attrib.Key)])
	case attribute.INT64:
		assert.Contains(t, measurements, string(attrib.Key))
		assert.Equal(t, float64(attrib.Value.AsInt64()), measurements[string(attrib.Key)])
	case attribute.FLOAT64:
		assert.Contains(t, measurements, string(attrib.Key))
		assert.Equal(t, attrib.Value.AsFloat64(), measurements[string(attrib.Key)])
	case attribute.BOOLSLICE, attribute.INT64SLICE, attribute.FLOAT64SLICE, attribute.STRINGSLICE:
		val, err := json.Marshal(attrib.Value.AsInterface())
		if err != nil {
			assert.Fail(t, fmt.Sprintf("value cannot be marshaled to JSON: %s", err.Error()))
		}
		assert.Contains(t, properties, string(attrib.Key))
		assert.Equal(t, string(val), properties[string(attrib.Key)])
	}
}

func getDefaultSpanStub() tracetest.SpanStub {
	traceId, _ := trace.TraceIDFromHex("68f1c4f4ef5346e69d7f196761d10c68")
	spanId, _ := trace.SpanIDFromHex("7fbdc197a52f4825877ddd46e4ec7f6c")
	parentSpanId, _ := trace.SpanIDFromHex("c73af03ea9684e67ab71bcff33b04a89")

	startTime, _ := time.Parse(time.RFC3339, "2006-01-02T15:04:05Z")
	endTime := startTime.Add(time.Second * 10)

	return tracetest.SpanStub{
		Name: "DefaultSpan",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceId,
			SpanID:  spanId,
		}),
		Parent: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: traceId,
			SpanID:  parentSpanId,
		}),
		SpanKind:  trace.SpanKindUnspecified,
		StartTime: startTime,
		EndTime:   endTime,
		Attributes: []attribute.KeyValue{
			// Types
			attribute.Bool("Bool", true),
			attribute.String("String", "StringVal"),
			attribute.Int64("Int64", 12345),
			attribute.Float64("Float64", 12345.12345),
			attribute.Float64Slice("Float64Slice", []float64{1.0, 2.0, 3.0}),
			attribute.Int64Slice("Int64Slice", []int64{1, 2, 3}),
			attribute.StringSlice("StringSlice", []string{"1", "2", "3"}),
			attribute.BoolSlice("BoolSlice", []bool{true, false, true}),

			// Special AI fields
			attribute.String(contracts.UserId, "UserId"),
			attribute.String(contracts.SessionId, "SessionId"),
		},
		Status: tracesdk.Status{Code: codes.Ok},
		Resource: resource.NewWithAttributes(
			semconv.SchemaURL,
			attribute.String("environment", "unit-test"),

			// Special AI fields
			attribute.String(contracts.ApplicationVersion, "0.1.0"),
		),
	}
}
