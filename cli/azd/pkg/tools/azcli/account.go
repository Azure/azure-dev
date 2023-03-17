package azcli

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

var (
	isNotLoggedInMessageRegex = regexp.MustCompile(
		`(ERROR: No subscription found)|(Please run ('|")az login('|") to (setup account|access your accounts)\.)+`,
	)
	isRefreshTokenExpiredMessageRegex = regexp.MustCompile(`AADSTS(70043|700082)`)
)

// AzCliAccessToken represents the value returned by `az account get-access-token`
type AzCliAccessToken struct {
	AccessToken string
	ExpiresOn   *time.Time
}

func (cli *azCli) GetAccessToken(ctx context.Context) (*AzCliAccessToken, error) {
	token, err := cli.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{
			fmt.Sprintf("%s/.default", cloud.AzurePublic.Services[cloud.ResourceManager].Audience),
		},
	})

	if err != nil {
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

func isNotLoggedInMessage(s string) bool {
	return isNotLoggedInMessageRegex.MatchString(s)
}

func isRefreshTokenExpiredMessage(s string) bool {
	return isRefreshTokenExpiredMessageRegex.MatchString(s)
}
