package graphsdk

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestGetServicePrincipalList(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := []ServicePrincipal{
			{
				Id:          convert.RefOf("1"),
				DisplayName: "SPN 1",
			},
			{
				Id:          convert.RefOf("2"),
				DisplayName: "SPN 2",
			},
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerServicePrincipalListMock(mockContext, http.StatusOK, expected)

		client, err := creatGraphClient(mockContext)
		require.NoError(t, err)

		servicePrincipals, err := client.ServicePrincipals().Get(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, servicePrincipals)
		require.Equal(t, expected, servicePrincipals.Value)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerServicePrincipalListMock(mockContext, http.StatusUnauthorized, nil)

		client, err := creatGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.ServicePrincipals().Get(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestGetServicePrincipalById(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := ServicePrincipal{
			Id:          convert.RefOf("1"),
			AppId:       "app-1",
			DisplayName: "App 1",
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerServicePrincipalItemMock(mockContext, http.StatusOK, *expected.Id, &expected)

		client, err := creatGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.ServicePrincipalById(*expected.Id).Get(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, expected.AppId, actual.AppId)
		require.Equal(t, expected.DisplayName, actual.DisplayName)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerServicePrincipalItemMock(mockContext, http.StatusNotFound, "bad-id", nil)

		client, err := creatGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.ServicePrincipalById("bad-id").Get(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, res)
	})
}

func TestCreateServicePrincipal(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := ServicePrincipal{
			Id:          convert.RefOf("1"),
			AppId:       "app-1",
			DisplayName: "App 1",
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerServicePrincipalCreateMock(mockContext, http.StatusCreated, &expected)

		client, err := creatGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.ServicePrincipals().Post(*mockContext.Context, &expected)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *expected.Id, *actual.Id)
		require.Equal(t, expected.AppId, actual.AppId)
		require.Equal(t, expected.DisplayName, actual.DisplayName)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerServicePrincipalCreateMock(mockContext, http.StatusBadRequest, nil)

		client, err := creatGraphClient(mockContext)
		require.NoError(t, err)

		res, err := client.ServicePrincipals().Post(*mockContext.Context, &ServicePrincipal{})
		require.Error(t, err)
		require.Nil(t, res)
	})
}

// Mock registration functions

func registerServicePrincipalListMock(mockContext *mocks.MockContext, statusCode int, servicePrincipals []ServicePrincipal) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/servicePrincipals")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		listResponse := ServicePrincipalListResponse{
			Value: servicePrincipals,
		}

		if servicePrincipals == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, listResponse)
	})
}

func registerServicePrincipalItemMock(
	mockContext *mocks.MockContext,
	statusCode int,
	spnId string,
	servicePrincipal *ServicePrincipal,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, fmt.Sprintf("/servicePrincipals/%s", spnId))
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if servicePrincipal == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, servicePrincipal)
	})
}

func registerServicePrincipalCreateMock(mockContext *mocks.MockContext, statusCode int, servicePrincipal *ServicePrincipal) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/servicePrincipals")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if servicePrincipal == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, servicePrincipal)
	})
}
