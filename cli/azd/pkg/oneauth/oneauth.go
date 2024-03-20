// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !(oneauth && windows)

package oneauth

import (
	"errors"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

var (
	// functions are defined as variables to make them easily replaceable in tests

	LogIn = func(authority, clientID, scope string) (string, error) {
		return "", errNotSupported
	}
	LogInSilently = func(clientID string) (string, error) {
		return "", errNotSupported
	}
	Logout = func(clientID string) error {
		return errNotSupported
	}
	NewCredential = func(authority, clientID string, opts CredentialOptions) (azcore.TokenCredential, error) {
		return nil, errNotSupported
	}
	Shutdown = func() {}

	// Supported indicates whether this build includes OneAuth integration.
	Supported = false

	errNotSupported = errors.New("this build doesn't support OneAuth authentication")
)
