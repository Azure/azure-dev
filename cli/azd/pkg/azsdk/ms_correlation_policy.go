package azsdk

import (
	"context"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"go.opentelemetry.io/otel/trace"
)

const cMsCorrelationIdHeader = "x-ms-correlation-request-id"

// msCorrelationNoOptPolicy is a no-opt policy. It doesn't do anything. It's used when no trace context exists.
type msCorrelationNoOptPolicy struct {
}

func (p *msCorrelationNoOptPolicy) Do(req *policy.Request) (*http.Response, error) {
	return req.Next()
}

type msCorrelationPolicy struct {
	correlationId string
}

// NewMsCorrelationPolicy creates a policy to ensure that Microsoft correlation ID headers are set on all HTTP requests.
// This works for Azure REST API, and could also work for other Microsoft-hosted services.
//
// Correlation IDs are taken from the existing trace context. If no trace context exists, then this policy is a no-op.
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

func (p *msCorrelationPolicy) Do(req *policy.Request) (*http.Response, error) {
	rawRequest := req.Raw()
	rawRequest.Header.Set(cMsCorrelationIdHeader, p.correlationId)

	return req.Next()
}
