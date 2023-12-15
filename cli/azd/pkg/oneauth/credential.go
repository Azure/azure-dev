// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !(windows && broker)

package oneauth

import (
	"errors"
)

// Supported indicates whether this build supports brokered authentication.
const Supported = false

var errNoBroker = errors.New("this build doesn't support brokered authentication")

func NewCredential(authority, clientID string, opts CredentialOptions) (UserCredential, error) {
	return nil, errNoBroker
}

func Logout(clientID string, debug bool) error {
	return errNoBroker
}

func SignIn(authority, clientID, homeAccountID, scope string, debug bool) (string, error) {
	return "", errNoBroker
}

func Start(clientID string, debug bool) error {
	return errNoBroker
}
