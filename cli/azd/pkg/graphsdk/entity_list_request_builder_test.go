package graphsdk_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/stretchr/testify/require"
)

func TestEntityListRequestBuilder(t *testing.T) {
	applications := []graphsdk.Application{
		{
			Id:          convert.RefOf("1"),
			DisplayName: "App 1",
		},
		{
			Id:          convert.RefOf("2"),
			DisplayName: "App 2",
		},
	}

	t.Run("WithProperties", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, applications)

		graphClient, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		expectedFilter := "displayName eq 'APPLICATION'"
		expectedTop := 10

		appRequestBuilder := graphsdk.NewApplicationListRequestBuilder(graphClient).
			Filter(expectedFilter).
			Top(expectedTop)

		var res *http.Response
		ctx := runtime.WithCaptureResponse(*mockContext.Context, &res)

		_, err = appRequestBuilder.Get(ctx)
		require.NoError(t, err)
		require.Equal(t, expectedFilter, res.Request.URL.Query().Get("$filter"))
		require.Equal(t, fmt.Sprint(expectedTop), res.Request.URL.Query().Get("$top"))
	})

	t.Run("NoProperties", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, applications)

		graphClient, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		appRequestBuilder := graphsdk.NewApplicationListRequestBuilder(graphClient)

		var res *http.Response
		ctx := runtime.WithCaptureResponse(*mockContext.Context, &res)

		_, err = appRequestBuilder.Get(ctx)
		require.NoError(t, err)
		require.Equal(t, "", res.Request.URL.RawQuery)
	})
}
