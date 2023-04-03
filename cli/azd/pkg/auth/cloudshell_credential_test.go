package auth

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

const cSuccessfulTokenResponse = `{
	"access_token": "sample-access-token",
	"refresh_token": "",
	"expires_in": "123",
	"expires_on": "123",
	"not_before": "123",
	"resource": "https://management.core.windows.net/",
	"token_type": "Bearer"
}`

func requireDefaultToken(t *testing.T, token azcore.AccessToken) {
	require.Equal(t, token.Token, "")
}

func TestCloudShellCredentialGetToken(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())

	cred := NewCloudShellCredential(mockContext.HttpClient)

	token, err := cred.GetToken(*mockContext.Context, policy.TokenRequestOptions{
		Scopes: []string{"one", "two"},
	})

	require.Error(t, err)
	requireDefaultToken(t, token)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return true
	}).Respond(&http.Response{
		StatusCode: 401,
		Body:       io.NopCloser(bytes.NewBufferString("some error")),
	})

	token, err = cred.GetToken(*mockContext.Context, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com//.default"},
	})

	require.Error(t, err)
	requireDefaultToken(t, token)

	mockContext.HttpClient.Reset()
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.URL.String() == "http://localhost:50342/oauth2/token" &&
			request.Method == http.MethodPost &&
			request.Header.Get("Content-Type") == "application/x-www-form-urlencoded" &&
			request.Header.Get("Metadata") == "true" &&
			request.FormValue("resource") == "https://management.azure.com/"
	}).Respond(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString(cSuccessfulTokenResponse)),
	})

	token, err = cred.GetToken(*mockContext.Context, policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com//.default"},
	})

	require.NoError(t, err)
	require.Equal(t, token.Token, "sample-access-token")
	require.Equal(t, token.ExpiresOn, time.Unix(123, 0))
}
