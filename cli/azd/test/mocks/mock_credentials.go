package mocks

import (
	"context"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type MockCredentials struct {
	GetTokenFn func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error)
}

func (c *MockCredentials) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if c.GetTokenFn != nil {
		return c.GetTokenFn(ctx, options)
	}

	return azcore.AccessToken{
		Token:     "ABC123",
		ExpiresOn: time.Now().Add(time.Hour * 1),
	}, nil
}
