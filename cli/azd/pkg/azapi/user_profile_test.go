package azapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GetUserAccessToken(t *testing.T) {
	expected := azcore.AccessToken{
		Token:     "ABC123",
		ExpiresOn: time.Now().Add(3 * time.Hour),
	}

	mockCredential := mocks.MockCredentials{
		GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
			return expected, nil
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	userProfile := NewUserProfileService(
		&mocks.MockMultiTenantCredentialProvider{
			TokenMap: map[string]mocks.MockCredentials{
				"": mockCredential,
			},
		},
		&azcore.ClientOptions{
			Transport: mockContext.HttpClient,
		},
		cloud.AzurePublic(),
	)

	actual, err := userProfile.GetAccessToken(*mockContext.Context, "")
	require.NoError(t, err)
	require.Equal(t, expected.Token, actual.AccessToken)
}

func Test_GetSignedInUserId(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
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

		userProfile := NewUserProfileService(
			&mocks.MockMultiTenantCredentialProvider{},
			&azcore.ClientOptions{
				Transport: mockContext.HttpClient,
			},
			cloud.AzurePublic(),
		)

		userId, err := userProfile.GetSignedInUserId(*mockContext.Context, "")
		require.NoError(t, err)
		require.Equal(t, mockUserProfile.Id, userId)
	})

	t.Run("Error", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		registerGetMeGraphMock(mockContext, http.StatusBadRequest, nil)

		userProfile := NewUserProfileService(
			&mocks.MockMultiTenantCredentialProvider{},
			&azcore.ClientOptions{
				Transport: mockContext.HttpClient,
			},
			cloud.AzurePublic(),
		)

		userId, err := userProfile.GetSignedInUserId(*mockContext.Context, "")
		require.Error(t, err)
		require.Equal(t, "", userId)
	})
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
