package graphsdk_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	graphsdk_mocks "github.com/azure/azure-dev/cli/azd/test/mocks/graphsdk"
	"github.com/stretchr/testify/require"
)

var (
	servicePrincipals []graphsdk.ServicePrincipal = []graphsdk.ServicePrincipal{
		{
			Id:          convert.RefOf("1"),
			DisplayName: "SPN 1",
		},
		{
			Id:          convert.RefOf("2"),
			DisplayName: "SPN 2",
		},
	}
)

func TestGetServicePrincipalList(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := append([]graphsdk.ServicePrincipal{}, servicePrincipals...)

		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterServicePrincipalListMock(mockContext, http.StatusOK, expected)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		servicePrincipals, err := client.
			ServicePrincipals().
			Get(*mockContext.Context)

		require.NoError(t, err)
		require.NotNil(t, servicePrincipals)
		require.Equal(t, expected, servicePrincipals.Value)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterServicePrincipalListMock(mockContext, http.StatusUnauthorized, nil)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			ServicePrincipals().
			Get(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestGetServicePrincipalById(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := servicePrincipals[0]

		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterServicePrincipalGetItemMock(mockContext, http.StatusOK, *expected.Id, &expected)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.
			ServicePrincipalById(*expected.Id).
			Get(*mockContext.Context)

		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, expected.AppId, actual.AppId)
		require.Equal(t, expected.DisplayName, actual.DisplayName)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterServicePrincipalGetItemMock(mockContext, http.StatusNotFound, "bad-id", nil)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			ServicePrincipalById("bad-id").
			Get(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestCreateServicePrincipal(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := servicePrincipals[0]

		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterServicePrincipalCreateItemMock(mockContext, http.StatusCreated, &expected)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.
			ServicePrincipals().
			Post(*mockContext.Context, &expected)

		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, expected.AppId, actual.AppId)
		require.Equal(t, expected.DisplayName, actual.DisplayName)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterServicePrincipalCreateItemMock(mockContext, http.StatusBadRequest, nil)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			ServicePrincipals().
			Post(*mockContext.Context, &graphsdk.ServicePrincipal{})

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestDeleteServicePrincipal(t *testing.T) {
	servicePrincipalId := "spn-to-delete"

	t.Run("Success", func(t *testing.T) {

		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterServicePrincipalDeleteItemMock(mockContext, servicePrincipalId, http.StatusNoContent)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ServicePrincipalById(servicePrincipalId).
			Delete(*mockContext.Context)

		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterServicePrincipalDeleteItemMock(mockContext, servicePrincipalId, http.StatusNotFound)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ServicePrincipalById(servicePrincipalId).
			Delete(*mockContext.Context)

		require.Error(t, err)
		var httpErr *azcore.ResponseError
		require.True(t, errors.As(err, &httpErr))
		require.Equal(t, http.StatusNotFound, httpErr.StatusCode)
	})
}
