// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !(oneauth && windows)

package oneauth

import "errors"

// Supported indicates whether this build includes OneAuth integration.
const Supported = false

var errNotSupported = errors.New("this build doesn't support OneAuth authentication")

func Logout(clientID string, debug bool) error {
	return errNotSupported
}

func NewCredential(authority, clientID string, opts CredentialOptions) (UserCredential, error) {
	return nil, errNotSupported
}

func SignIn(authority, clientID, homeAccountID, scope string, debug bool) (string, error) {
	return "", errNotSupported
}

func Start(clientID string, debug bool) error {
	return errNotSupported
}
