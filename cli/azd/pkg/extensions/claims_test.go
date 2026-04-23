// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func Test_GetClaimsFromContext_WithValidClaims(t *testing.T) {
	claims := &ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: "test-extension",
			Issuer:  "azd",
		},
		Capabilities: []CapabilityType{CustomCommandCapability},
	}

	ctx := WithClaimsContext(context.Background(), claims)
	retrieved, err := GetClaimsFromContext(ctx)
	require.NoError(t, err)
	require.NotNil(t, retrieved)
	require.Equal(t, "test-extension", retrieved.Subject)
	require.Equal(t, "azd", retrieved.Issuer)
	require.Contains(t, retrieved.Capabilities, CustomCommandCapability)
}

func Test_GetClaimsFromContext_WithoutClaims_ReturnsError(t *testing.T) {
	ctx := context.Background()
	claims, err := GetClaimsFromContext(ctx)
	require.Error(t, err)
	require.Nil(t, claims)
	require.Contains(t, err.Error(), "no validated extension claims found in context")
}

func Test_GetClaimsFromContext_NilClaims_ReturnsError(t *testing.T) {
	ctx := context.WithValue(context.Background(), extensionClaimsKey, (*ExtensionClaims)(nil))
	claims, err := GetClaimsFromContext(ctx)
	require.Error(t, err)
	require.Nil(t, claims)
	require.Contains(t, err.Error(), "no validated extension claims found in context")
}
