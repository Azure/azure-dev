package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// UserProfileService allows querying for user profile information.
type UserProfileService struct {
	credentialProvider auth.MultiTenantCredentialProvider
	userAgent          string
	httpClient         httputil.HttpClient
}

func NewUserProfileService(
	credentialProvider auth.MultiTenantCredentialProvider,
	httpClient httputil.HttpClient) *UserProfileService {
	return &UserProfileService{
		userAgent:          azdinternal.MakeUserAgentString(""),
		httpClient:         httpClient,
		credentialProvider: credentialProvider,
	}
}

func (u *UserProfileService) createGraphClient(ctx context.Context) (*graphsdk.GraphClient, error) {
	options := clientOptionsBuilder(u.httpClient, u.userAgent).BuildCoreClientOptions()
	cred, err := u.credentialProvider.GetTokenCredential(ctx, "")
	if err != nil {
		return nil, err
	}

	client, err := graphsdk.NewGraphClient(cred, options)
	if err != nil {
		return nil, fmt.Errorf("creating Graph Users client: %w", err)
	}

	return client, nil
}

func (user *UserProfileService) GetSignedInUserId(ctx context.Context) (*string, error) {
	client, err := user.createGraphClient(ctx)
	if err != nil {
		return nil, err
	}

	userProfile, err := client.Me().Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving current user profile: %w", err)
	}

	return &userProfile.Id, nil
}

func (u *UserProfileService) GetAccessToken(ctx context.Context) (*AzCliAccessToken, error) {
	cred, err := u.credentialProvider.GetTokenCredential(ctx, "")
	if err != nil {
		return nil, err
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{
			fmt.Sprintf("%s/.default", cloud.AzurePublic.Services[cloud.ResourceManager].Audience),
		},
	})

	if err != nil {
		// This could happen currently if auth returned an azcli credential underneath
		if isNotLoggedInMessage(err.Error()) {
			return nil, ErrAzCliNotLoggedIn
		} else if isRefreshTokenExpiredMessage(err.Error()) {
			return nil, ErrAzCliRefreshTokenExpired
		}

		return nil, fmt.Errorf("failed retrieving access token: %w", err)
	}

	return &AzCliAccessToken{
		AccessToken: token.Token,
		ExpiresOn:   &token.ExpiresOn,
	}, nil
}
