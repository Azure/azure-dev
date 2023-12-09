// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !(windows && broker)

package oneauth

import (
	"context"
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// Supported indicates whether this build supports brokered authentication.
const Supported = false

var errNoBroker = errors.New("this build doesn't support brokered authentication")

type credential struct{}

func NewCredential(authority, clientID string, opts CredentialOptions) (UserCredential, error) {
	return nil, errNoBroker
}

func (*credential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, errNoBroker
}

func (*credential) HomeAccountID() string {
	return ""
}

func (c *credential) Logout() error {
	return errNoBroker
}
