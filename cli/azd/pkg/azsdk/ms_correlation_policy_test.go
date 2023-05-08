package azsdk

import (
	"context"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

var traceId trace.TraceID

// 0-bytes hex is by default invalid
var invalidTraceId trace.TraceID

func init() {
	var err error
	traceId, err = trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		panic(err)
	}
}

func Test_msCorrelationPolicy_Do(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		expect *string
	}{
		{
			name:   "WithTraceId",
			ctx:    trace.ContextWithSpanContext(context.Background(), trace.SpanContext{}.WithTraceID(traceId)),
			expect: convert.RefOf(traceId.String()),
		},
		{
			name:   "WithInvalidTraceId",
			ctx:    trace.ContextWithSpanContext(context.Background(), trace.SpanContext{}.WithTraceID(invalidTraceId)),
			expect: convert.RefOf(""),
		},
		{
			name:   "WithoutTraceId",
			ctx:    context.Background(),
			expect: nil,
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
				WithPerCallPolicy(NewMsCorrelationPolicy(tt.ctx)).
				BuildArmClientOptions()

			client, err := armresources.NewClient("SUBSCRIPTION_ID", &mocks.MockCredentials{}, clientOptions)
			require.NoError(t, err)

			var response *http.Response
			ctx := runtime.WithCaptureResponse(tt.ctx, &response)

			_, _ = client.GetByID(ctx, "RESOURCE_ID", "", nil)

			if tt.expect != nil {
				require.Equal(t, response.Request.Header.Get(cCorrelationIdHeader), *tt.expect)
			} else {
				for header := range response.Request.Header {
					if header == cCorrelationIdHeader {
						require.Fail(t, "should not contain correlation id header")
					}
				}
			}

		})
	}
}
