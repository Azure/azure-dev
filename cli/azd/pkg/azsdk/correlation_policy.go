package azsdk

import (
	"context"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"go.opentelemetry.io/otel/trace"
)

// See https://github.com/Azure/azure-resource-manager-rpc/blob/master/v1.0/common-api-details.md#client-request-headers
const cMsCorrelationIdHeader = "x-ms-correlation-request-id"

// See https://learn.microsoft.com/en-us/graph/best-practices-concept#reliability-and-support
const cMsGraphCorrelationIdHeader = "client-request-id"

// noOptPolicy is a no-opt policy. It doesn't do anything. It's used when no trace context exists.
type noOptPolicy struct {
}

func (p *noOptPolicy) Do(req *policy.Request) (*http.Response, error) {
	return req.Next()
}

// simpleCorrelationPolicy is a policy that sets a simple correlation ID HTTP header.
type simpleCorrelationPolicy struct {
	correlationId string
	header        string
}

func (p *simpleCorrelationPolicy) Do(req *policy.Request) (*http.Response, error) {
	rawRequest := req.Raw()
	rawRequest.Header.Set(p.header, p.correlationId)

	return req.Next()
}

// NewMsCorrelationPolicy creates a policy that sets Microsoft correlation ID headers on HTTP requests.
// This works for Azure REST API, and could also work for other Microsoft-hosted services that do not yet honor distributed
// tracing.
//
// Correlation IDs are taken from the existing trace context. If no trace context exists, then this policy is a no-op.
func NewMsCorrelationPolicy(ctx context.Context) policy.Policy {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.HasTraceID() {
		return &noOptPolicy{}
	}

	policy := &simpleCorrelationPolicy{}
	policy.correlationId = spanCtx.TraceID().String()
	policy.header = cMsCorrelationIdHeader
	return policy
}

// NewMsGraphCorrelationPolicy creates a policy that sets Microsoft Graph correlation ID headers on HTTP requests.
func NewMsGraphCorrelationPolicy(ctx context.Context) policy.Policy {
	spanCtx := trace.SpanContextFromContext(ctx)
	if !spanCtx.HasTraceID() {
		return &noOptPolicy{}
	}

	policy := &simpleCorrelationPolicy{}
	policy.correlationId = spanCtx.TraceID().String()
	policy.header = cMsGraphCorrelationIdHeader
	return policy
}
