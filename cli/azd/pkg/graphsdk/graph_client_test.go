package graphsdk_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/stretchr/testify/require"
)

// Testing simulates requests that have a pre-flight error like
// acquiring token or DNS issues (host not found)
func Test_GraphClientRequest_With_Preflight_Error(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	client, err := mockgraphsdk.CreateGraphClient(mockContext)
	require.NoError(t, err)
	require.NotNil(t, client)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == "/v1.0/me"
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("some error before request could be made")
	})

	res, err := client.Me().Get(*mockContext.Context)
	require.Nil(t, res)
	require.Error(t, err)
}
