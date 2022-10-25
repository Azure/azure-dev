package graphsdk_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	graphsdk_mocks "github.com/azure/azure-dev/cli/azd/test/mocks/graphsdk"
	"github.com/stretchr/testify/require"
)

func TestGetApplicationList(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := []graphsdk.Application{
			{
				Id:          convert.RefOf("1"),
				DisplayName: "App 1",
			},
			{
				Id:          convert.RefOf("2"),
				DisplayName: "App 2",
			},
		}

		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationListMock(mockContext, http.StatusOK, expected)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		apps, err := client.Applications().Get(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, apps)
		require.Equal(t, expected, apps.Value)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationListMock(mockContext, http.StatusUnauthorized, nil)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.Applications().Get(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestGetApplicationById(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := graphsdk.Application{
			Id:                  convert.RefOf("1"),
			AppId:               convert.RefOf("app-1"),
			DisplayName:         "App 1",
			PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{},
		}

		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationItemMock(mockContext, http.StatusOK, *expected.Id, &expected)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.ApplicationById(*expected.Id).Get(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, *expected.AppId, *actual.AppId)
		require.Equal(t, expected.DisplayName, actual.DisplayName)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationItemMock(mockContext, http.StatusNotFound, "bad-id", nil)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.ApplicationById("bad-id").Get(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestCreateApplication(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := graphsdk.Application{
			Id:                  convert.RefOf("1"),
			AppId:               convert.RefOf("app-1"),
			DisplayName:         "App 1",
			PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{},
		}

		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationCreateMock(mockContext, http.StatusCreated, &expected)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.Applications().Post(*mockContext.Context, &expected)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, *expected.AppId, *actual.AppId)
		require.Equal(t, expected.DisplayName, actual.DisplayName)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationCreateMock(mockContext, http.StatusBadRequest, nil)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.Applications().Post(*mockContext.Context, &graphsdk.Application{})
		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestApplicationAddPassword(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		app := graphsdk.Application{
			Id:                  convert.RefOf("1"),
			AppId:               convert.RefOf("app-1"),
			DisplayName:         "App 1",
			PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{},
		}

		mockCredential := graphsdk.ApplicationPasswordCredential{
			KeyId:       convert.RefOf("key1"),
			DisplayName: convert.RefOf("Name"),
			SecretText:  convert.RefOf("foobar"),
		}

		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationAddPasswordMock(mockContext, http.StatusOK, *app.Id, &mockCredential)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.ApplicationById(*app.Id).AddPassword(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *mockCredential.KeyId, *actual.KeyId)
		require.Equal(t, *mockCredential.DisplayName, *actual.DisplayName)
		require.Equal(t, mockCredential.SecretText, actual.SecretText)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationAddPasswordMock(mockContext, http.StatusNotFound, "bad-app-id", nil)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.ApplicationById("bad-app-id").AddPassword(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, actual)
	})
}

func TestApplicationRemovePassword(t *testing.T) {
	app := graphsdk.Application{
		Id:                  convert.RefOf("1"),
		AppId:               convert.RefOf("app-1"),
		DisplayName:         "App 1",
		PasswordCredentials: []*graphsdk.ApplicationPasswordCredential{},
	}

	t.Run("Success", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNoContent, *app.Id)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.ApplicationById(*app.Id).RemovePassword(*mockContext.Context, "key1")
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		graphsdk_mocks.RegisterApplicationRemovePasswordMock(mockContext, http.StatusNotFound, *app.Id)

		client, err := graphsdk_mocks.CreateGraphClient(mockContext)
		require.NoError(t, err)

		err = client.ApplicationById(*app.Id).RemovePassword(*mockContext.Context, "bad-key-id")
		require.Error(t, err)
	})
}
