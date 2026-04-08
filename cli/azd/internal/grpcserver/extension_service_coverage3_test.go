// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestNewExtensionService(t *testing.T) {
	t.Parallel()
	svc := NewExtensionService(nil)
	require.NotNil(t, svc)
}

func TestExtensionService_Ready_MissingClaims(t *testing.T) {
	t.Parallel()
	svc := NewExtensionService(nil)
	_, err := svc.Ready(t.Context(), &azdext.ReadyRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "extension claims")
}

func TestExtensionService_ReportError_MissingClaims(t *testing.T) {
	t.Parallel()
	svc := NewExtensionService(nil)
	_, err := svc.ReportError(t.Context(), &azdext.ReportErrorRequest{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "extension claims")
}

// --- Extension Token Round-trip ---

func TestGenerateAndParseExtensionToken_RoundTrip(t *testing.T) {
	t.Parallel()
	key, err := generateSigningKey()
	require.NoError(t, err)

	info := &ServerInfo{
		Address:    "localhost:8080",
		SigningKey: key,
	}

	ext := &extensions.Extension{
		Id:           "test-ext",
		Capabilities: []extensions.CapabilityType{"cap1", "cap2"},
	}

	tokenStr, err := GenerateExtensionToken(ext, info)
	require.NoError(t, err)
	require.NotEmpty(t, tokenStr)

	claims, err := ParseExtensionToken(tokenStr, info)
	require.NoError(t, err)
	require.Equal(t, "test-ext", claims.Subject)
	require.Equal(t, []extensions.CapabilityType{"cap1", "cap2"}, claims.Capabilities)
}

func TestParseExtensionToken_InvalidToken(t *testing.T) {
	t.Parallel()
	key, err := generateSigningKey()
	require.NoError(t, err)

	info := &ServerInfo{
		Address:    "localhost:8080",
		SigningKey: key,
	}

	_, err = ParseExtensionToken("invalid.token.value", info)
	require.Error(t, err)
	require.Contains(t, err.Error(), "token validation failed")
}

func TestParseExtensionToken_WrongKey(t *testing.T) {
	t.Parallel()
	key1, err := generateSigningKey()
	require.NoError(t, err)
	key2, err := generateSigningKey()
	require.NoError(t, err)

	info1 := &ServerInfo{Address: "localhost:8080", SigningKey: key1}
	info2 := &ServerInfo{Address: "localhost:8080", SigningKey: key2}

	ext := &extensions.Extension{Id: "test-ext"}
	tokenStr, err := GenerateExtensionToken(ext, info1)
	require.NoError(t, err)

	_, err = ParseExtensionToken(tokenStr, info2)
	require.Error(t, err)
}
