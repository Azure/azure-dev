package azsdk

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
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

func Test_simpleCorrelationPolicy_Do(t *testing.T) {
	tests := []struct {
		name                  string
		ctx                   context.Context
		expect                *string
		headerName            string
		correlationPolicyFunc func() policy.Policy
	}{
		{
			name: "WithTraceId",
			ctx: trace.ContextWithSpanContext(
				context.Background(),
				trace.SpanContext{}.WithTraceID(traceId),
			),
			expect:                convert.RefOf(traceId.String()),
			headerName:            cMsCorrelationIdHeader,
			correlationPolicyFunc: NewMsCorrelationPolicy,
		},
		{
			name: "WithInvalidTraceId",
			// nolint:lll
			ctx: trace.ContextWithSpanContext(
				context.Background(),
				trace.SpanContext{}.WithTraceID(invalidTraceId),
			),
			expect:                convert.RefOf(""),
			headerName:            cMsCorrelationIdHeader,
			correlationPolicyFunc: NewMsCorrelationPolicy,
		},
		{
			name:                  "WithoutTraceId",
			ctx:                   context.Background(),
			expect:                nil,
			headerName:            cMsCorrelationIdHeader,
			correlationPolicyFunc: NewMsCorrelationPolicy,
		},
		{
			name: "WithTraceId",
			ctx: trace.ContextWithSpanContext(
				context.Background(),
				trace.SpanContext{}.WithTraceID(traceId),
			),
			expect:                convert.RefOf(traceId.String()),
			headerName:            cMsGraphCorrelationIdHeader,
			correlationPolicyFunc: NewMsGraphCorrelationPolicy,
		},
		{
			name: "WithInvalidTraceId",
			// nolint:lll
			ctx: trace.ContextWithSpanContext(
				context.Background(),
				trace.SpanContext{}.WithTraceID(invalidTraceId),
			),
			expect:                convert.RefOf(""),
			headerName:            cMsGraphCorrelationIdHeader,
			correlationPolicyFunc: NewMsGraphCorrelationPolicy,
		},
		{
			name:                  "WithoutTraceId",
			ctx:                   context.Background(),
			expect:                nil,
			headerName:            cMsGraphCorrelationIdHeader,
			correlationPolicyFunc: NewMsGraphCorrelationPolicy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpClient := mockhttp.NewMockHttpUtil()
			httpClient.When(func(request *http.Request) bool {
				return true
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				return mocks.CreateEmptyHttpResponse(request, http.StatusOK)
			})

			clientOptions := NewClientOptionsBuilder().
				WithTransport(httpClient).
				WithPerCallPolicy(tt.correlationPolicyFunc()).
				BuildArmClientOptions()

			client, err := armresources.NewClient("SUBSCRIPTION_ID", &mocks.MockCredentials{}, clientOptions)
			require.NoError(t, err)

			var response *http.Response
			ctx := runtime.WithCaptureResponse(tt.ctx, &response)

			_, _ = client.GetByID(ctx, "RESOURCE_ID", "", nil)

			if tt.expect != nil {
				require.Equal(t, *tt.expect, response.Request.Header.Get(tt.headerName))
			} else {
				for header := range response.Request.Header {
					if header == tt.headerName {
						require.Fail(t, "should not contain correlation id header")
					}
				}
			}
		})
	}
}
