// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test that case variations are handled
func TestCheckLocalhostOrigin_CaseSensitivity(t *testing.T) {
	tests := []struct {
		name     string
		origin   string
		expected bool
	}{
		{"lowercase localhost", "http://localhost:8080", true},
		{"uppercase LOCALHOST", "http://LOCALHOST:8080", true},  // Hostname comparison should be case-insensitive.
		{"mixed case LocAlHost", "http://LocAlHost:8080", true}, // Hostname comparison should be case-insensitive.
		{"uppercase 127.0.0.1", "HTTP://127.0.0.1:8080", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Origin", tt.origin)
			result := checkLocalhostOrigin(req)
			require.Equal(t, tt.expected, result, "origin: %s", tt.origin)
		})
	}
}
