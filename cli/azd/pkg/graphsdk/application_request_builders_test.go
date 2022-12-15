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
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
	"github.com/stretchr/testify/require"
)

var (
	applications []graphsdk.Application = []graphsdk.Application{
		{
			Id:          convert.RefOf("1"),
			AppId:       convert.RefOf("app-01"),
			DisplayName: "App 1",
		},
		{
			Id:          convert.RefOf("2"),
			AppId:       convert.RefOf("app-02"),
			DisplayName: "App 2",
		},
	}
)

func TestGetApplicationList(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := append([]graphsdk.Application{}, applications...)

		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusOK, expected)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		apps, err := client.Applications().Get(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, apps)
		require.Equal(t, expected, apps.Value)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationListMock(mockContext, http.StatusUnauthorized, nil)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			Applications().
			Get(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestGetApplicationById(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := applications[0]

		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationGetItemMock(mockContext, http.StatusOK, *expected.Id, &expected)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.
			ApplicationById(*expected.Id).
			Get(*mockContext.Context)

		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, *expected.AppId, *actual.AppId)
		require.Equal(t, expected.DisplayName, actual.DisplayName)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationGetItemMock(mockContext, http.StatusNotFound, "bad-id", nil)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			ApplicationById("bad-id").
			Get(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestCreateApplication(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := applications[0]

		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationCreateItemMock(mockContext, http.StatusCreated, &expected)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.
			Applications().
			Post(*mockContext.Context, &expected)

		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, *expected.AppId, *actual.AppId)
		require.Equal(t, expected.DisplayName, actual.DisplayName)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationCreateItemMock(mockContext, http.StatusBadRequest, nil)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.
			Applications().
			Post(*mockContext.Context, &graphsdk.Application{})

		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestDeleteApplication(t *testing.T) {
	applicationId := "app-to-delete"

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationDeleteItemMock(mockContext, applicationId, http.StatusNoContent)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ApplicationById(applicationId).
			Delete(*mockContext.Context)

		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationDeleteItemMock(mockContext, applicationId, http.StatusNotFound)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ApplicationById(applicationId).
			Delete(*mockContext.Context)

		require.Error(t, err)
		var httpErr *azcore.ResponseError
		require.True(t, errors.As(err, &httpErr))
		require.Equal(t, http.StatusNotFound, httpErr.StatusCode)
	})
}

func TestApplicationAddPassword(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		application := applications[0]

		mockCredential := graphsdk.ApplicationPasswordCredential{
			KeyId:       convert.RefOf("key1"),
			DisplayName: convert.RefOf("Name"),
			SecretText:  convert.RefOf("foobar"),
		}

		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *application.Id, &mockCredential)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.
			ApplicationById(*application.Id).
			AddPassword(*mockContext.Context)

		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *mockCredential.KeyId, *actual.KeyId)
		require.Equal(t, *mockCredential.DisplayName, *actual.DisplayName)
		require.Equal(t, mockCredential.SecretText, actual.SecretText)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationAddPasswordMock(mockContext, http.StatusNotFound, "bad-app-id", nil)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.
			ApplicationById("bad-app-id").
			AddPassword(*mockContext.Context)

		require.Error(t, err)
		require.Nil(t, actual)
	})
}

func TestApplicationRemovePassword(t *testing.T) {
	application := applications[0]

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNoContent, *application.Id)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.
			ApplicationById(*application.Id).
			RemovePassword(*mockContext.Context, "key1")

		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockgraphsdk.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNotFound, *application.Id)

		client, err := mockgraphsdk.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.ApplicationById(*application.Id).RemovePassword(*mockContext.Context, "bad-key-id")
		require.Error(t, err)
	})
}
