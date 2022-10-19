package graphsdk

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestGetMe(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		expected := UserProfile{
			Id:                "user1",
			GivenName:         "John",
			Surname:           "Doe",
			JobTitle:          "Software Engineer",
			DisplayName:       "John Doe",
			UserPrincipalName: "john.doe@contoso.com",
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerMeGetMock(mockContext, http.StatusOK, &expected)

		client, err := creatGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.Me().Get(*mockContext.Context)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.Equal(t, *actual, expected)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerMeGetMock(mockContext, http.StatusUnauthorized, nil)

		client, err := creatGraphClient(mockContext)
		require.NoError(t, err)

		actual, err := client.Me().Get(*mockContext.Context)
		require.Error(t, err)
		require.Nil(t, actual)
	})
}

func registerMeGetMock(mockContext *mocks.MockContext, statusCode int, userProfile *UserProfile) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/me")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if userProfile == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, userProfile)
	})
}
