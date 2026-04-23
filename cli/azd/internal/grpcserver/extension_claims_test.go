// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func Test_GenerateSigningKey_Length(t *testing.T) {
	key, err := generateSigningKey()
	require.NoError(t, err)
	require.Len(t, key, 32, "signing key must be 32 bytes (256-bit)")
}

func Test_GenerateParse_TokenClaims(t *testing.T) {
	signingKey, err := generateSigningKey()
	require.NoError(t, err)

	serverInfo := &ServerInfo{
		Address:    "localhost:1234",
		Port:       1234,
		SigningKey: signingKey,
	}

	extension := &extensions.Extension{
		Id:        "microsoft.azd.test",
		Namespace: "test",
		Capabilities: []extensions.CapabilityType{
			extensions.CustomCommandCapability,
			extensions.LifecycleEventsCapability,
		},
		DisplayName: "Test",
		Version:     "0.0.1",
	}

	token, err := GenerateExtensionToken(extension, serverInfo)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	t.Run("Valid", func(t *testing.T) {
		claims, err := ParseExtensionToken(token, serverInfo)
		require.NoError(t, err)
		require.NotNil(t, claims)

		require.Equal(t, serverInfo.Address, claims.Audience[0])
		require.Equal(t, extension.Id, claims.Subject)
	})

	t.Run("Invalid", func(t *testing.T) {
		invalidSigningKey, err := generateSigningKey()
		require.NoError(t, err)

		invalidServerInfo := &ServerInfo{
			Address:    "localhost:1234",
			Port:       1234,
			SigningKey: invalidSigningKey,
		}

		claims, err := ParseExtensionToken(token, invalidServerInfo)
		require.Error(t, err)
		require.Nil(t, claims)
	})
}

// Test_ParseExtensionToken_AlgorithmSubstitution verifies that a token signed
// with RS256 (asymmetric) is rejected by the HMAC-only key function guard.
func Test_ParseExtensionToken_AlgorithmSubstitution(t *testing.T) {
	signingKey, err := generateSigningKey()
	require.NoError(t, err)

	serverInfo := &ServerInfo{
		Address:    "localhost:5678",
		Port:       5678,
		SigningKey: signingKey,
	}

	// Generate an RSA key and sign a token with RS256 — the parser must reject this.
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	claims := extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azd",
			Subject:   "microsoft.azd.evil",
			Audience:  []string{serverInfo.Address},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
	}

	rs256Token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(rsaKey)
	require.NoError(t, err)

	parsed, err := ParseExtensionToken(rs256Token, serverInfo)
	require.Error(t, err)
	require.Nil(t, parsed)
	require.Contains(t, err.Error(), "unexpected signing method")
}

// Test_ParseExtensionToken_AlgorithmSubstitution_HS384 verifies that a token signed
// with HS384 (a valid HMAC variant, but not the pinned HS256) is rejected.
func Test_ParseExtensionToken_AlgorithmSubstitution_HS384(t *testing.T) {
	signingKey, err := generateSigningKey()
	require.NoError(t, err)

	serverInfo := &ServerInfo{
		Address:    "localhost:5678",
		Port:       5678,
		SigningKey: signingKey,
	}

	claims := extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azd",
			Subject:   "microsoft.azd.evil",
			Audience:  []string{serverInfo.Address},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
	}

	// Sign with HS384 — a valid HMAC variant but not the pinned HS256
	hs384Token, err := jwt.NewWithClaims(jwt.SigningMethodHS384, claims).SignedString(signingKey)
	require.NoError(t, err)

	parsed, err := ParseExtensionToken(hs384Token, serverInfo)
	require.Error(t, err)
	require.Nil(t, parsed)
	require.Contains(t, err.Error(), "unexpected signing method")
}

// Test_ParseExtensionToken_TamperedToken verifies that a structurally valid
// token whose payload has been tampered with is rejected (signature mismatch).
func Test_ParseExtensionToken_TamperedToken(t *testing.T) {
	signingKey, err := generateSigningKey()
	require.NoError(t, err)

	serverInfo := &ServerInfo{
		Address:    "localhost:5678",
		Port:       5678,
		SigningKey: signingKey,
	}

	ext := &extensions.Extension{
		Id:        "microsoft.azd.test",
		Namespace: "test",
		Capabilities: []extensions.CapabilityType{
			extensions.CustomCommandCapability,
		},
		DisplayName: "Test",
		Version:     "0.0.1",
	}

	token, err := GenerateExtensionToken(ext, serverInfo)
	require.NoError(t, err)

	// Tamper with the token: flip a character in the payload segment (middle part)
	parts := strings.SplitN(token, ".", 3)
	require.Len(t, parts, 3)

	// Corrupt the payload by replacing the first char
	payload := []byte(parts[1])
	if payload[0] == 'A' {
		payload[0] = 'B'
	} else {
		payload[0] = 'A'
	}
	tampered := parts[0] + "." + string(payload) + "." + parts[2]

	parsed, err := ParseExtensionToken(tampered, serverInfo)
	require.Error(t, err)
	require.Nil(t, parsed)
}

// Test_ParseExtensionToken_ExpiredToken verifies that an expired token is
// rejected by the expiration check inside ParseExtensionToken.
func Test_ParseExtensionToken_ExpiredToken(t *testing.T) {
	signingKey, err := generateSigningKey()
	require.NoError(t, err)

	serverInfo := &ServerInfo{
		Address:    "localhost:5678",
		Port:       5678,
		SigningKey: signingKey,
	}

	// Create a token that expired 2 hours ago
	claims := extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "azd",
			Subject:   "microsoft.azd.expired",
			Audience:  []string{serverInfo.Address},
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-3 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
		Capabilities: []extensions.CapabilityType{extensions.CustomCommandCapability},
	}

	expiredToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(signingKey)
	require.NoError(t, err)

	parsed, err := ParseExtensionToken(expiredToken, serverInfo)
	require.Error(t, err)
	require.Nil(t, parsed)
}
