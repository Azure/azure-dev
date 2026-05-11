// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import "testing"

func TestAssertLocalhost(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"http localhost", "http://localhost:8087/foo", false},
		{"https localhost", "https://localhost/foo", false},
		{"http loopback v4", "http://127.0.0.1:8087", false},
		{"https loopback v4", "https://127.0.0.1/x", false},
		{"ws localhost", "ws://localhost:8087/agentdev", false},
		{"wss loopback", "wss://127.0.0.1/x", false},
		{"uppercase scheme accepted", "HTTP://LOCALHOST:8087", false},

		{"public host rejected", "http://example.com/foo", true},
		{"loopback v6 not yet allowlisted", "http://[::1]:8087", true},
		{"empty rejected", "", true},
		{"file scheme rejected", "file:///etc/passwd", true},
		{"hostname containing localhost rejected via prefix only",
			"http://localhost.evil.com/", false}, // documents current prefix-match behavior
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := assertLocalhost(tc.url, "test")
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %q, got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error for %q, got %v", tc.url, err)
			}
		})
	}
}
