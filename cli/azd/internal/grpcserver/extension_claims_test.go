// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"testing"

	"github.com/azure/azure-dev/pkg/extensions"
	"github.com/stretchr/testify/require"
)

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
