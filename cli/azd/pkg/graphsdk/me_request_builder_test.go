package graphsdk_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/stretchr/testify/require"
)

func TestGetMe(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := graphsdk.UserProfile{
			Id:                "user1",
			GivenName:         "John",
			Surname:           "Doe",
			JobTitle:          "Software Engineer",
			DisplayName:       "John Doe",
			UserPrincipalName: "john.doe@contoso.com",
		}

		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterMeGetMock(mockContext, http.StatusOK, &expected)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.Me().Get(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *actual, expected)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterMeGetMock(mockContext, http.StatusUnauthorized, nil)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.Me().Get(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, actual)
	})
}
