// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
)

var (
	ErrAzCliNotLoggedIn         = errors.New("cli is not logged in. Try running \"az login\" to fix")
	ErrAzCliRefreshTokenExpired = errors.New("refresh token has expired. Try running \"az login\" to fix")
)

func NewAzureClient(
	credentialProvider account.SubscriptionCredentialProvider,
	armClientOptions *arm.ClientOptions,
) *AzureClient {
	return &AzureClient{
		credentialProvider: credentialProvider,
		armClientOptions:   armClientOptions,
	}
}

type AzureClient struct {
	credentialProvider account.SubscriptionCredentialProvider
	armClientOptions   *arm.ClientOptions
}
