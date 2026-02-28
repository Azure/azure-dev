// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

type mockHttpClient struct {
	lastRequest *http.Request
}

func (m *mockHttpClient) Do(req *http.Request) (*http.Response, error) {
	m.lastRequest = req
	return &http.Response{StatusCode: 200}, nil
}

func (m *mockHttpClient) CloseIdleConnections() {}

func TestUserAgentClient(t *testing.T) {
	tests := []struct {
		name              string
		userAgent         string
		existingUserAgent string
		nilHeader         bool
		expectedUserAgent string
		expectWrapped     bool
	}{
		{
			name:              "SetsUserAgentWhenEmpty",
			userAgent:         "azdev/1.0.0",
			existingUserAgent: "",
			expectedUserAgent: "azdev/1.0.0",
			expectWrapped:     true,
		},
		{
			name:              "AppendsToExistingUserAgent",
			userAgent:         "azdev/1.0.0",
			existingUserAgent: "existing-agent/2.0",
			expectedUserAgent: "existing-agent/2.0,azdev/1.0.0",
			expectWrapped:     true,
		},
		{
			name:          "EmptyUserAgentReturnsInnerClient",
			userAgent:     "",
			expectWrapped: false,
		},
		{
			name:              "HandlesNilHeader",
			userAgent:         "azdev/1.0.0",
			nilHeader:         true,
			expectedUserAgent: "azdev/1.0.0",
			expectWrapped:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &mockHttpClient{}
			client := newUserAgentClient(inner, tt.userAgent)

			if !tt.expectWrapped {
				// Should return the inner client unchanged
				require.Equal(t, inner, client)
				return
			}

			req, err := http.NewRequest("GET", "https://example.com", nil)
			require.NoError(t, err)

			if tt.nilHeader {
				req.Header = nil
			} else if tt.existingUserAgent != "" {
				req.Header.Set("User-Agent", tt.existingUserAgent)
			}

			_, err = client.Do(req)
			require.NoError(t, err)
			require.Equal(t, tt.expectedUserAgent, inner.lastRequest.Header.Get("User-Agent"))
		})
	}
}
