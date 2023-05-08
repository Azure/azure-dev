package azsdk

import (
	"context"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"go.opentelemetry.io/otel/trace"
)

const cCorrelationIdHeader = "x-ms-correlation-request-id"

// msCorrelationNoOptPolicy is a no-opt policy. It doesn't do anything. It's used when no trace context exists.
type msCorrelationNoOptPolicy struct {
}

func (p *msCorrelationNoOptPolicy) Do(req *policy.Request) (*http.Response, error) {
	return req.Next()
}

type msCorrelationPolicy struct {
	correlationId string
}

// Policy to ensure that 'x-ms-correlation-request-id' header is set on all HTTP requests.
//
// If a trace context exists, the trace ID is used as the correlation ID.
func NewMsCorrelationPolicy(ctx context.Context) policy.Policy {
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanCtx.HasTraceID() {
		policy := &msCorrelationPolicy{}
		policy.correlationId = spanCtx.TraceID().String()
		return policy
	} else {
		return &msCorrelationNoOptPolicy{}
	}
}

// Sets the correlation ID string on the underlying request
func (p *msCorrelationPolicy) Do(req *policy.Request) (*http.Response, error) {
	rawRequest := req.Raw()
	rawRequest.Header.Set(cCorrelationIdHeader, p.correlationId)

	return req.Next()
}
