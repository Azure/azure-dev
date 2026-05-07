// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

// See https://github.com/Azure/azure-resource-manager-rpc/blob/master/v1.0/common-api-details.md#client-request-headers
const MsCorrelationIdHeader = "x-ms-correlation-request-id"
const MsClientRequestIdHeader = "x-ms-client-request-id"

// See https://learn.microsoft.com/en-us/graph/best-practices-concept#reliability-and-support
const msGraphCorrelationIdHeader = "client-request-id"

// traceCorrelationPolicy is a policy that sets a correlation ID HTTP header derived from the ambient OpenTelemetry
// trace ID. The Azure ARM spec defines `x-ms-correlation-request-id` as a session-level header meant to group
// RELATED requests; deriving it from the root trace ID intentionally matches that semantic so operators can pivot
// from a single azd invocation to every service-side log line it produced.
type traceCorrelationPolicy struct {
	headerName string
}

func (p *traceCorrelationPolicy) Do(req *policy.Request) (*http.Response, error) {
	rawRequest := req.Raw()
	spanCtx := trace.SpanContextFromContext(rawRequest.Context())
	if !spanCtx.HasTraceID() {
		return req.Next()
	}

	rawRequest.Header.Set(p.headerName, spanCtx.TraceID().String())
	return req.Next()
}

// perRequestUUIDPolicy sets a header to a freshly generated UUID on every outgoing HTTP request. It is used for
// headers the Azure / Microsoft Graph specs require to be unique PER REQUEST (for example `x-ms-client-request-id`
// and Graph's `client-request-id`), so downstream services can rely on them as deduplication / idempotency /
// scratch keys without parallel calls from a single azd invocation colliding.
type perRequestUUIDPolicy struct {
	headerName string
}

func (p *perRequestUUIDPolicy) Do(req *policy.Request) (*http.Response, error) {
	req.Raw().Header.Set(p.headerName, uuid.NewString())
	return req.Next()
}

// NewMsCorrelationPolicy creates a policy that sets the `x-ms-correlation-request-id` header on HTTP requests using
// the ambient OpenTelemetry trace ID. Per the Azure ARM common-types spec this header is session-level and is
// intended to correlate RELATED requests, so a single value shared across every call in an azd command is the
// correct behavior.
//
// NOTE: One ARM data plane — ACR `GetBuildSourceUploadURL` — derives a blob path from this header and therefore
// requires uniqueness per call. That collision is fixed at the call site (see `containerregistry.RemoteBuildManager`)
// by overriding this header with a fresh UUID for the ACR upload client only. See `containerregistry/remote_build.go`.
func NewMsCorrelationPolicy() policy.Policy {
	return &traceCorrelationPolicy{headerName: MsCorrelationIdHeader}
}

// NewMsClientRequestIdPolicy creates a policy that sets the `x-ms-client-request-id` header on HTTP requests.
//
// The Azure ARM common-types spec requires this header to be UNIQUE PER REQUEST so Azure services can log and
// diagnose individual calls. Using a shared value (for example the ambient trace ID, as azd previously did) breaks
// any downstream service that uses the header as a deduplication / idempotency / scratch key across parallel
// requests. Accordingly we generate a fresh UUID on every `Do` invocation.
func NewMsClientRequestIdPolicy() policy.Policy {
	return &perRequestUUIDPolicy{headerName: MsClientRequestIdHeader}
}

// NewMsGraphCorrelationPolicy creates a policy that sets Microsoft Graph's `client-request-id` header on HTTP
// requests. Graph's reliability guidance asks callers to supply a unique value per request so individual calls
// can be traced through Graph's pipeline; we therefore generate a fresh UUID on every `Do` invocation.
func NewMsGraphCorrelationPolicy() policy.Policy {
	return &perRequestUUIDPolicy{headerName: msGraphCorrelationIdHeader}
}
