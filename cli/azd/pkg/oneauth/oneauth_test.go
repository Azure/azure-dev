// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !(oneauth && windows)

package oneauth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogin(t *testing.T) {
	_, err := LogIn("authority", "clientID", "scope")
	require.ErrorIs(t, err, errNotSupported)
}

func TestLogout(t *testing.T) {
	err := Logout("clientID")
	require.ErrorIs(t, err, errNotSupported)
}

func TestNewCredential(t *testing.T) {
	cred, err := NewCredential("authority", "clientID", CredentialOptions{})
	require.ErrorIs(t, err, errNotSupported)
	require.Nil(t, cred)
}

func TestSupported(t *testing.T) {
	require.False(t, Supported)
}
