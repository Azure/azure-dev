// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"fmt"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"go.opentelemetry.io/otel/trace"
)

// See https://github.com/Azure/azure-resource-manager-rpc/blob/master/v1.0/common-api-details.md#client-request-headers
const MsCorrelationIdHeader = "x-ms-correlation-request-id"
const MsClientRequestIdHeader = "x-ms-client-request-id"

// See https://learn.microsoft.com/en-us/graph/best-practices-concept#reliability-and-support
const msGraphCorrelationIdHeader = "client-request-id"

// simpleCorrelationPolicy is a policy that sets a simple correlation ID HTTP header.
type simpleCorrelationPolicy struct {
	headerName string
}

func (p *simpleCorrelationPolicy) Do(req *policy.Request) (*http.Response, error) {
	rawRequest := req.Raw()
	spanCtx := trace.SpanContextFromContext(rawRequest.Context())
	if !spanCtx.HasTraceID() {
		return req.Next()
	}

	rawRequest.Header.Set(p.headerName, spanCtx.TraceID().String())
	fmt.Printf("[azsdk] Setting %s: %s\n", p.headerName, spanCtx.TraceID().String())
	return req.Next()
}

// NewMsCorrelationPolicy creates a policy that sets Microsoft correlation ID headers on HTTP requests.
// This works for Azure REST API, and could also work for other Microsoft-hosted services that do not yet honor distributed
// tracing.
func NewMsCorrelationPolicy() policy.Policy {
	return &simpleCorrelationPolicy{headerName: MsCorrelationIdHeader}
}

// NewMsClientRequestIdPolicy creates a policy that sets the x-ms-client-request-id header on HTTP requests.
// This is used by Azure services to log and correlate individual requests for diagnostics.
func NewMsClientRequestIdPolicy() policy.Policy {
	return &simpleCorrelationPolicy{headerName: MsClientRequestIdHeader}
}

// NewMsGraphCorrelationPolicy creates a policy that sets Microsoft Graph correlation ID headers on HTTP requests.
func NewMsGraphCorrelationPolicy() policy.Policy {
	return &simpleCorrelationPolicy{headerName: msGraphCorrelationIdHeader}
}
