package mocks

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type MockCredentials struct{}

func (c *MockCredentials) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "ABC123",
		ExpiresOn: time.Now().Add(time.Hour * 1),
	}, nil
}
