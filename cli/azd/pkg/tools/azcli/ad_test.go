package azcli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GetSignedInUserId(t *testing.T) {
	mockUserProfile := graphsdk.UserProfile{
		Id:                "user1",
		GivenName:         "John",
		Surname:           "Doe",
		JobTitle:          "Software Engineer",
		DisplayName:       "John Doe",
		UserPrincipalName: "john.doe@contoso.com",
	}

	mockContext := mocks.NewMockContext(context.Background())
	registerGetMeGraphMock(mockContext, http.StatusOK, &mockUserProfile)

	azCli := GetAzCli(*mockContext.Context)

	userId, err := azCli.GetSignedInUserId(*mockContext.Context)
	require.NoError(t, err)
	require.Equal(t, mockUserProfile.Id, userId)
}

func Test_CreateOrUpdateServicePrincipal(t *testing.T) {
}

func registerGetMeGraphMock(mockContext *mocks.MockContext, statusCode int, userProfile *graphsdk.UserProfile) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/me")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if userProfile == nil {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}

		return mocks.CreateHttpResponseWithBody(request, statusCode, userProfile)
	})
}
