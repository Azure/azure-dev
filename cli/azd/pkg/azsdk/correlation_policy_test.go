// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azsdk

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

var traceId trace.TraceID

// The default trace.TraceID which is a 0-bytes hex that is invalid
var invalidTraceId trace.TraceID

func init() {
	var err error
	traceId, err = trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		panic(err)
	}
}

// doRequest issues a single request through a mock client wired to `p` and returns the `http.Request` that the
// mock transport observed (so tests can inspect the outgoing headers).
func doRequest(t *testing.T, p policy.Policy, ctx context.Context) *http.Request {
	t.Helper()

	httpClient := mockhttp.NewMockHttpUtil()
	httpClient.When(func(request *http.Request) bool {
		return true
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, http.StatusOK)
	})

	client, err := armresources.NewClient("SUBSCRIPTION_ID", &mocks.MockCredentials{}, &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			PerCallPolicies: []policy.Policy{p},
			Transport:       httpClient,
		},
	})
	require.NoError(t, err)

	var response *http.Response
	reqCtx := policy.WithCaptureResponse(ctx, &response)
	_, _ = client.GetByID(reqCtx, "RESOURCE_ID", "", nil)
	require.NotNil(t, response, "mock transport did not capture a response")
	return response.Request
}

// Test_NewMsCorrelationPolicy verifies that the session-level `x-ms-correlation-request-id` header is set from
// the ambient OpenTelemetry trace ID when present, and omitted otherwise. Using the trace ID is semantically
// correct here: the ARM spec defines this header as session-level and meant to correlate RELATED requests.
func Test_NewMsCorrelationPolicy(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		expect *string
	}{
		{
			name: "WithTraceId",
			ctx: trace.ContextWithSpanContext(
				t.Context(),
				trace.SpanContext{}.WithTraceID(traceId),
			),
			expect: new(traceId.String()),
		},
		{
			name: "WithInvalidTraceId",
			ctx: trace.ContextWithSpanContext(
				t.Context(),
				trace.SpanContext{}.WithTraceID(invalidTraceId),
			),
			expect: new(""),
		},
		{
			name:   "WithoutTraceId",
			ctx:    t.Context(),
			expect: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := doRequest(t, NewMsCorrelationPolicy(), tt.ctx)
			if tt.expect != nil {
				require.Equal(t, *tt.expect, req.Header.Get(MsCorrelationIdHeader))
			} else {
				require.Empty(t, req.Header.Get(MsCorrelationIdHeader))
			}
		})
	}
}

// Test_NewMsClientRequestIdPolicy_UniquePerRequest verifies `x-ms-client-request-id` is a valid UUID on every
// call and is distinct across calls, regardless of whether a trace context is present. The previous
// implementation derived this header from the ambient trace ID, which collided across parallel requests within a
// single azd command and broke any Azure service that uses the header as a deduplication / idempotency / scratch key
// (per Azure ARM spec this header must be unique per request).
func Test_NewMsClientRequestIdPolicy_UniquePerRequest(t *testing.T) {
	assertPerRequestUniqueUUID(t, NewMsClientRequestIdPolicy(), MsClientRequestIdHeader)
}

// Test_NewMsGraphCorrelationPolicy_UniquePerRequest verifies the same per-request-unique UUID contract for
// Microsoft Graph's `client-request-id` header.
func Test_NewMsGraphCorrelationPolicy_UniquePerRequest(t *testing.T) {
	assertPerRequestUniqueUUID(t, NewMsGraphCorrelationPolicy(), msGraphCorrelationIdHeader)
}

// assertPerRequestUniqueUUID runs `p` through three separate HTTP calls and asserts:
//  1. each call's `header` value parses as a UUID;
//  2. the values are pairwise distinct (proving the policy neither caches nor derives the value from shared state
//     such as the ambient trace ID);
//  3. when a trace ID IS present, the emitted value still differs from it — regression test for the previous
//     implementation that fell back to the trace ID on every call.
func assertPerRequestUniqueUUID(t *testing.T, p policy.Policy, header string) {
	t.Helper()

	ctxWithTrace := trace.ContextWithSpanContext(
		context.Background(),
		trace.SpanContext{}.WithTraceID(traceId),
	)
	id1 := doRequest(t, p, ctxWithTrace).Header.Get(header)
	_, parseErr := uuid.Parse(id1)
	require.NoError(t, parseErr, "header %q must be a valid UUID, got %q", header, id1)
	require.NotEqual(t, traceId.String(), id1,
		"header %q must NOT be derived from the ambient trace ID (per-request uniqueness required)", header)

	id2 := doRequest(t, p, context.Background()).Header.Get(header)
	_, parseErr = uuid.Parse(id2)
	require.NoError(t, parseErr, "header %q must be a valid UUID on every call, got %q", header, id2)
	require.NotEqual(t, id1, id2,
		"header %q must be unique per request; got identical values across two calls", header)

	id3 := doRequest(t, p, context.Background()).Header.Get(header)
	require.NotEqual(t, id1, id3)
	require.NotEqual(t, id2, id3)
}
