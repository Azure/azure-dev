package azcli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_UserAgent_Policy(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == "/RESOURCE_ID"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armresources.ClientGetByIDResponse{
			GenericResource: armresources.GenericResource{
				ID:       convert.RefOf("RESOURCE_ID"),
				Kind:     convert.RefOf("RESOURCE_KIND"),
				Name:     convert.RefOf("RESOURCE_NAME"),
				Type:     convert.RefOf("RESOURCE_TYPE"),
				Location: convert.RefOf("RESOURCE_LOCATION"),
			},
		}

		responseJson, err := json.Marshal(response)
		if err != nil {
			return nil, err
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Request:    request,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(responseJson)),
		}, nil
	})

	var rawResponse *http.Response
	ctx := runtime.WithCaptureResponse(*mockContext.Context, &rawResponse)

	azCli := GetAzCli(ctx)
	// We don't care about the actual response or if an error occurred
	// Any API call that leverages the Go SDK is fine
	_, _ = azCli.GetResource(ctx, "SUBSCRIPTION_ID", "RESOURCE_ID")

	userAgent, ok := rawResponse.Request.Header["User-Agent"]
	if !ok {
		require.Fail(t, "missing User-Agent header")
	}

	require.Contains(t, userAgent[0], "azsdk-go")
	require.Contains(t, userAgent[0], "azdev")
}
