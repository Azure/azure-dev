package graphsdk

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestGetApplicationList(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := []Application{
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
		registerApplicationListMock(mockContext, http.StatusOK, expected)

		client, err := createGraphClient(mockContext)
		require.NoError(t, err)

		apps, err := client.Applications().Get(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, apps)
		require.Equal(t, expected, apps.Value)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerApplicationListMock(mockContext, http.StatusUnauthorized, nil)

		client, err := createGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.Applications().Get(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestGetApplicationById(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := Application{
			Id:                  convert.RefOf("1"),
			AppId:               convert.RefOf("app-1"),
			DisplayName:         "App 1",
			PasswordCredentials: []*ApplicationPasswordCredential{},
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerApplicationItemMock(mockContext, http.StatusOK, *expected.Id, &expected)

		client, err := createGraphClient(mockContext)
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
		registerApplicationItemMock(mockContext, http.StatusNotFound, "bad-id", nil)

		client, err := createGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.ApplicationById("bad-id").Get(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestCreateApplication(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := Application{
			Id:                  convert.RefOf("1"),
			AppId:               convert.RefOf("app-1"),
			DisplayName:         "App 1",
			PasswordCredentials: []*ApplicationPasswordCredential{},
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerApplicationCreateMock(mockContext, http.StatusCreated, &expected)

		client, err := createGraphClient(mockContext)
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
		registerApplicationCreateMock(mockContext, http.StatusBadRequest, nil)

		client, err := createGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.Applications().Post(*mockContext.Context, &Application{})
		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestApplicationAddPassword(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		app := Application{
			Id:                  convert.RefOf("1"),
			AppId:               convert.RefOf("app-1"),
			DisplayName:         "App 1",
			PasswordCredentials: []*ApplicationPasswordCredential{},
		}

		mockCredential := ApplicationPasswordCredential{
			KeyId:       convert.RefOf("key1"),
			DisplayName: convert.RefOf("Name"),
			SecretText:  convert.RefOf("foobar"),
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerApplicationAddPasswordMock(mockContext, http.StatusOK, *app.Id, &mockCredential)

		client, err := createGraphClient(mockContext)
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
		registerApplicationAddPasswordMock(mockContext, http.StatusNotFound, "bad-app-id", nil)

		client, err := createGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.ApplicationById("bad-app-id").AddPassword(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, actual)
	})
}

func TestApplicationRemovePassword(t *testing.T) {
	app := Application{
		Id:                  convert.RefOf("1"),
		AppId:               convert.RefOf("app-1"),
		DisplayName:         "App 1",
		PasswordCredentials: []*ApplicationPasswordCredential{},
	}

	t.Run("Success", func(t *testing.T) {
		mockCredential := ApplicationPasswordCredential{
			KeyId:       convert.RefOf("key1"),
			DisplayName: convert.RefOf("Name"),
			SecretText:  convert.RefOf("foobar"),
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerApplicationRemovePasswordMock(mockContext, http.StatusNoContent, *app.Id, &mockCredential)

		client, err := createGraphClient(mockContext)
		require.NoError(t, err)

		err = client.ApplicationById(*app.Id).RemovePassword(*mockContext.Context, "key1")
		require.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerApplicationRemovePasswordMock(mockContext, http.StatusNotFound, *app.Id, nil)

		client, err := createGraphClient(mockContext)
		require.NoError(t, err)

		err = client.ApplicationById(*app.Id).RemovePassword(*mockContext.Context, "bad-key-id")
		require.Error(t, err)
	})
}

// Mock registration functions

func registerApplicationListMock(mockContext *mocks.MockContext, statusCode int, applications []Application) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/applications")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		listResponse := ApplicationListResponse{
			Value: applications,
		}

		if applications == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, listResponse)
	})
}

func registerApplicationItemMock(mockContext *mocks.MockContext, statusCode int, appId string, application *Application) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s", appId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if application == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, application)
	})
}

func registerApplicationCreateMock(mockContext *mocks.MockContext, statusCode int, application *Application) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/applications")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if application == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, application)
	})
}

func registerApplicationAddPasswordMock(
	mockContext *mocks.MockContext,
	statusCode int,
	appId string,
	credential *ApplicationPasswordCredential,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s/addPassword", appId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if credential == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, credential)
	})
}

func registerApplicationRemovePasswordMock(
	mockContext *mocks.MockContext,
	statusCode int,
	appId string,
	credential *ApplicationPasswordCredential,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/applications/%s/removePassword", appId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if credential == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, credential)
	})
}

func createGraphClient(mockContext *mocks.MockContext) (*GraphClient, error) {
	credential := identity.GetCredentials(*mockContext.Context)
	clientOptions := createDefaultClientOptions(mockContext)

	return NewGraphClient(credential, clientOptions)
}

func createDefaultClientOptions(mockContext *mocks.MockContext) *azcore.ClientOptions {
	return azsdk.NewClientOptionsBuilder().
		WithTransport(mockContext.HttpClient).
		BuildCoreClientOptions()
}
