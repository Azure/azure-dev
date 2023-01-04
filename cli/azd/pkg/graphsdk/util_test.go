package graphsdk_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/stretchr/testify/require"
)

// Tests whether the requests executed with the pipeline run with a bearer token policy
// and are called with the correct scopes
func TestNewPipeline(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return true
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(request, http.StatusOK)
	})

	var actualScopes []string

	ran := false
	expectedScopes := []string{
		fmt.Sprintf("%s/.default", graphsdk.ServiceConfig.Audience),
	}

	credential := mocks.MockCredentials{
		GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
			ran = true
			actualScopes = options.Scopes

			return azcore.AccessToken{
				Token:     "ABC123",
				ExpiresOn: time.Now().Add(time.Hour * 1),
			}, nil
		},
	}

	clientOptions := mockgraphsdk.CreateDefaultClientOptions(mockContext)
	pipeline := graphsdk.NewPipeline(&credential, graphsdk.ServiceConfig, clientOptions)
	require.False(t, ran)
	require.NotNil(t, pipeline)

	req, err := runtime.NewRequest(*mockContext.Context, http.MethodGet, graphsdk.ServiceConfig.Endpoint)
	require.NoError(t, err)

	_, err = pipeline.Do(req)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, expectedScopes, actualScopes)
}
