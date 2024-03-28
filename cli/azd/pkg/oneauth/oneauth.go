// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !(oneauth && windows)

package oneauth

import (
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

// Supported indicates whether this build includes OneAuth integration.
const Supported = false

var errNotSupported = errors.New("this build doesn't support OneAuth authentication")

func LogIn(authority, clientID, scope string) (string, error) {
	return "", errNotSupported
}

func LogInSilently(clientID string) (string, error) {
	return "", errNotSupported
}

func Logout(clientID string) error {
	return errNotSupported
}

func NewCredential(authority, clientID string, opts CredentialOptions) (azcore.TokenCredential, error) {
	return nil, errNotSupported
}

func Shutdown() {}
