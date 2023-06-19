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

type MockMultiTenantCredentialProvider struct {
	TokenMap map[string]MockCredentials
}

func (c *MockMultiTenantCredentialProvider) GetTokenCredential(
	ctx context.Context, tenantId string) (azcore.TokenCredential, error) {
	if c.TokenMap != nil {
		tokenCred := c.TokenMap[tenantId]
		return &tokenCred, nil
	}

	return &MockCredentials{
		GetTokenFn: func(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
			return azcore.AccessToken{
				Token:     tenantId,
				ExpiresOn: time.Now().Add(time.Hour * 1),
			}, nil
		},
	}, nil
}

type MockSubscriptionCredentialProvider struct {
}

func (scp *MockSubscriptionCredentialProvider) CredentialForSubscription(
	ctx context.Context,
	subscriptionId string,
) (azcore.TokenCredential, error) {
	return &MockCredentials{}, nil
}
