// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_IsLocalhostAddress(t *testing.T) {
	tests := []struct {
		name     string
		address  string
		expected bool
	}{
		{"localhost with port", "localhost:8080", true},
		{"127.0.0.1 with port", "127.0.0.1:3000", true},
		{"ipv6 loopback with port", "[::1]:8080", true},
		{"localhost without port", "localhost", true},
		{"127.0.0.1 without port", "127.0.0.1", true},
		{"external hostname", "example.com:8080", false},
		{"external IP", "192.168.1.1:8080", false},
		{"empty string", "", false},
		{"loopback IP 127.0.0.2", "127.0.0.2:8080", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLocalhostAddress(tt.address)
			require.Equal(t, tt.expected, result)
		})
	}
}
