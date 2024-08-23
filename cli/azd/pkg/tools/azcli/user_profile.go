package azcli

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	azcloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/auth"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
)

// UserProfileService allows querying for user profile information.
type UserProfileService struct {
	credentialProvider auth.MultiTenantCredentialProvider
	coreClientOptions  *azcore.ClientOptions
	cloud              *cloud.Cloud
	authManager        *auth.Manager
}

func NewUserProfileService(
	credentialProvider auth.MultiTenantCredentialProvider,
	coreClientOptions *azcore.ClientOptions,
	cloud *cloud.Cloud,
	authManager *auth.Manager,
) *UserProfileService {
	return &UserProfileService{
		credentialProvider: credentialProvider,
		coreClientOptions:  coreClientOptions,
		cloud:              cloud,
		authManager:        authManager,
	}
}

func (u *UserProfileService) createGraphClient(ctx context.Context, tenantId string) (*graphsdk.GraphClient, error) {
	cred, err := u.credentialProvider.GetTokenCredential(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	client, err := graphsdk.NewGraphClient(cred, u.coreClientOptions)
	if err != nil {
		return nil, fmt.Errorf("creating Graph Users client: %w", err)
	}

	return client, nil
}

func (user *UserProfileService) GetSignedInUserId(ctx context.Context, tenantId string) (string, error) {
	userProfile, err := user.SignedProfile(ctx, tenantId)
	if err != nil {
		return "", err
	}

	return userProfile.Id, nil
}

func (user *UserProfileService) SignedProfile(ctx context.Context, tenantId string) (*graphsdk.UserProfile, error) {
	client, err := user.createGraphClient(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	userProfile, err := client.Me().Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving current user profile: %w", err)
	}

	return userProfile, nil
}

func (user *UserProfileService) AppProfile(
	ctx context.Context, tenantId string) (*graphsdk.Application, error) {
	client, err := user.createGraphClient(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	appId, err := user.authManager.GetLoggedInServicePrincipalID(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting logged in service principal ID: %w", err)
	}

	appProfile, err := client.ApplicationById(*appId).GetByAppId(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed retrieving current user profile: %w", err)
	}

	return appProfile, nil
}

func (u *UserProfileService) GetAccessToken(ctx context.Context, tenantId string) (*AzCliAccessToken, error) {
	cred, err := u.credentialProvider.GetTokenCredential(ctx, tenantId)
	if err != nil {
		return nil, err
	}

	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{
			fmt.Sprintf("%s/.default", u.cloud.Configuration.Services[azcloud.ResourceManager].Audience),
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
