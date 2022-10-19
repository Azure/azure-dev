package graphsdk

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestEntityListRequestBuilder(t *testing.T) {
	t.Run("WithProperties", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphClient, err := createGraphClient(mockContext)
		require.NoError(t, err)

		expectedFilter := "displayName eq 'APPLICATION'"
		expectedTop := 10

		appRequestBuilder := newApplicationsRequestBuilder(graphClient).
			Filter(expectedFilter).
			Top(expectedTop)

		req, err := appRequestBuilder.createRequest(*mockContext.Context, http.MethodGet, serviceConfig.Endpoint)
		require.NoError(t, err)
		require.Equal(t, expectedFilter, req.Raw().URL.Query().Get("$filter"))
		require.Equal(t, fmt.Sprint(expectedTop), req.Raw().URL.Query().Get("$top"))
	})

	t.Run("NoProperties", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphClient, err := createGraphClient(mockContext)
		require.NoError(t, err)

		appRequestBuilder := newApplicationsRequestBuilder(graphClient)

		req, err := appRequestBuilder.createRequest(*mockContext.Context, http.MethodGet, serviceConfig.Endpoint)
		require.NoError(t, err)
		require.Equal(t, "", req.Raw().URL.RawQuery)
	})
}
