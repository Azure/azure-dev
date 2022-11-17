package azcli

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GetAccessToken(t *testing.T) {
	expected := azcore.AccessToken{
		Token:     "ABC123",
		ExpiresOn: time.Now().Add(1 * time.Hour),
	}

	mockCredential := mocks.MockCredentials{
		GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
			return expected, nil
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	azCli := NewAzCli(&mockCredential, NewAzCliArgs{
		HttpClient: mockContext.HttpClient,
	})

	actual, err := azCli.GetAccessToken(*mockContext.Context)
	require.NoError(t, err)
	require.Equal(t, expected.Token, actual.AccessToken)
	require.Equal(t, expected.ExpiresOn, *actual.ExpiresOn)
}
