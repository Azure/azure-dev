// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdministratorNameFromToken(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"preferred_username":"developer@example.com","upn":"other@example.com"}`),
	)
	name, err := administratorNameFromToken("header." + payload + ".signature")
	require.NoError(t, err)
	require.Equal(t, "developer@example.com", name)
}

func TestAdministratorNameFromTokenSupportsGuestLogin(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"upn":"developer_contoso.com#EXT#@tenant.onmicrosoft.com"}`),
	)
	name, err := administratorNameFromToken("header." + payload + ".signature")
	require.NoError(t, err)
	require.Equal(t, "developer_contoso.com#EXT#@tenant.onmicrosoft.com", name)
}

func TestAdministratorNameFromTokenRejectsMissingOrInvalidClaims(t *testing.T) {
	for _, token := range []string{
		"not-a-token",
		"header.not-base64.signature",
		"header.e30.signature",
	} {
		_, err := administratorNameFromToken(token)
		require.Error(t, err)
	}
}
