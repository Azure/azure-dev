package appinsightsexporter

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/microsoft/ApplicationInsights-Go/appinsights/contracts"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
)

// Converts an OpenTelemetry span into an ApplicationInsights envelope suitable for transmission.
func SpanToEnvelope(span trace.ReadOnlySpan) *contracts.Envelope {
	envelope := contracts.NewEnvelope()
	envelope.Tags = make(map[string]string)
	envelope.Time = span.StartTime().Format(time.RFC3339Nano)
	envelope.Tags[contracts.OperationId] = span.SpanContext().TraceID().String()
	envelope.Tags[contracts.OperationParentId] = span.Parent().SpanID().String()

	requestData, contextTags := spanToRequestData(span)
	data := contracts.NewData()
	data.BaseData = requestData
	data.BaseType = requestData.BaseType()

	envelope.Data = data
	envelope.Name = requestData.EnvelopeName("")
	envelope.Tags[contracts.OperationName] = requestData.Name

	for contextKey, contextVal := range contextTags {
		envelope.Tags[contextKey] = contextVal
	}

	// Sanitize.
	for _, warn := range envelope.Sanitize() {
		diagLog.Printf("Telemetry data warning: %s", warn)
	}

	for _, warn := range contracts.SanitizeTags(envelope.Tags) {
		diagLog.Printf("Telemetry tag warning: %s", warn)
	}

	return envelope
}

// Converts an OpenTelemetry span into an ApplicationInsights RequestTelemetry data,
// returning remainder attributes that are meant to used as context tags.
func spanToRequestData(span trace.ReadOnlySpan) (*contracts.RequestData, map[string]string) {
	contextTags := map[string]string{}
	requestData := contracts.NewRequestData()
	requestData.Id = span.SpanContext().SpanID().String()
	requestData.Name = span.Name()
	requestData.Source = ""
	requestData.Duration = formatDuration(span.EndTime().Sub(span.StartTime()))
	requestData.Success = span.Status().Code != codes.Error
	requestData.Url = ""
	requestData.Properties = map[string]string{}
	requestData.Measurements = map[string]float64{}

	if span.Status().Code == codes.Error {
		if len(span.Status().Description) > 0 {
			requestData.ResponseCode = span.Status().Description
		} else {
			requestData.ResponseCode = "UnknownFailure"
		}
	} else {
		requestData.ResponseCode = "Success"
	}

	for _, resourceAttrib := range span.Resource().Attributes() {
		if isResourceContextTagField(string(resourceAttrib.Key)) {
			contextTags[string(resourceAttrib.Key)] = resourceAttrib.Value.Emit()
		} else {
			SetAttributeAsPropertyOrMeasurement(resourceAttrib, requestData.Properties, requestData.Measurements)
		}
	}

	for _, attrib := range span.Attributes() {
		if isAttributeContextTagField(string(attrib.Key)) {
			contextTags[string(attrib.Key)] = attrib.Value.Emit()
		} else {
			SetAttributeAsPropertyOrMeasurement(attrib, requestData.Properties, requestData.Measurements)
		}
	}

	for _, warn := range requestData.Sanitize() {
		diagLog.Printf("Request telemetry data warning: %s", warn)
	}

	return requestData, contextTags
}

func isResourceContextTagField(key string) bool {
	return key == contracts.ApplicationVersion
}

var attributeContextTagKeys = map[string]bool{
	// Currently, we only support user and session related info.
	// The full list of context tag keys can be found in the appinsights-go library.
	contracts.UserAccountId:  true,
	contracts.UserAuthUserId: true,
	contracts.UserId:         true,
	contracts.SessionId:      true,
	contracts.SessionIsFirst: true,
}

func isAttributeContextTagField(key string) bool {
	return attributeContextTagKeys[key]
}

func SetAttributeAsPropertyOrMeasurement(
	kv attribute.KeyValue,
	properties map[string]string,
	measurements map[string]float64) {

	switch kv.Value.Type() {
	case attribute.BOOL, attribute.STRING:
		properties[string(kv.Key)] = kv.Value.Emit()
	case attribute.INT64:
		measurements[string(kv.Key)] = float64(kv.Value.AsInt64())
	case attribute.FLOAT64:
		measurements[string(kv.Key)] = kv.Value.AsFloat64()
	case attribute.BOOLSLICE, attribute.INT64SLICE, attribute.FLOAT64SLICE, attribute.STRINGSLICE:
		arrayJson, err := json.Marshal(kv.Value.AsInterface())
		if err != nil {
			diagLog.Printf("Could not serialize slice of type '%s' as JSON array: %s", kv.Value.Type(), err.Error())
			return
		}
		properties[string(kv.Key)] = string(arrayJson)
	default:
		diagLog.Printf("Telemetry data warning, unknown type: %s", kv.Value.Type())
		return
	}
}

func formatDuration(d time.Duration) string {
	ticks := int64(d/(time.Nanosecond*100)) % 10000000
	seconds := int64(d/time.Second) % 60
	minutes := int64(d/time.Minute) % 60
	hours := int64(d/time.Hour) % 24
	days := int64(d / (time.Hour * 24))

	return fmt.Sprintf("%d.%02d:%02d:%02d.%07d", days, hours, minutes, seconds, ticks)
}
