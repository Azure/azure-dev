// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkey

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionProjectEndpoint(t *testing.T) {
	t.Parallel()

	// Keep these wire-format vectors in sync with the Agents consumer tests.
	tests := map[string]string{
		"search":         "CONNECTION_V2_736561726368_PROJECT_ENDPOINT",
		"my connection":  "CONNECTION_V2_6D7920636F6E6E656374696F6E_PROJECT_ENDPOINT",
		"my--connection": "CONNECTION_V2_6D792D2D636F6E6E656374696F6E_PROJECT_ENDPOINT",
		"A":              "CONNECTION_V2_41_PROJECT_ENDPOINT",
		"a":              "CONNECTION_V2_61_PROJECT_ENDPOINT",
	}
	for name, expected := range tests {
		assert.Equal(t, expected, ConnectionProjectEndpoint(name))
	}
}

func TestConnectionProjectEndpointPreservesExactServiceName(t *testing.T) {
	t.Parallel()

	seen := map[string]string{}
	for _, name := range []string{"", "my connection", "my--connection", "my_connection", "my.connection", "A", "a", "\u00e9"} {
		key := ConnectionProjectEndpoint(name)
		assert.Regexp(t, `^[A-Z][A-Z0-9_]*$`, key)
		// Environment keys are case-insensitive on Windows.
		normalized := strings.ToUpper(key)
		previous, exists := seen[normalized]
		require.False(t, exists, "service %q collides with %q", name, previous)
		seen[normalized] = name
		encoded := strings.TrimSuffix(strings.TrimPrefix(key, "CONNECTION_V2_"), "_PROJECT_ENDPOINT")
		decoded, err := hex.DecodeString(encoded)
		require.NoError(t, err)
		assert.Equal(t, name, string(decoded))
	}
}
