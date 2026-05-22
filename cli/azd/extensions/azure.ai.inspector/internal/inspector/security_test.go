// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"testing"
)

func TestValidateAgentProxyURL(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		agentPort int
		wantErr   bool
	}{
		{
			name:      "localhost http",
			url:       "http://localhost:8088/responses",
			agentPort: 8088,
		},
		{
			name:      "ipv4 loopback https",
			url:       "https://127.0.0.1:8088/responses",
			agentPort: 8088,
		},
		{
			name:      "ipv6 loopback",
			url:       "http://[::1]:8088/responses",
			agentPort: 8088,
		},
		{
			name:      "non local host",
			url:       "http://example.com:8088/responses",
			agentPort: 8088,
			wantErr:   true,
		},
		{
			name:      "metadata endpoint",
			url:       "http://169.254.169.254:8088/metadata/identity",
			agentPort: 8088,
			wantErr:   true,
		},
		{
			name:      "wrong port",
			url:       "http://localhost:8089/responses",
			agentPort: 8088,
			wantErr:   true,
		},
		{
			name:      "missing port",
			url:       "http://localhost/responses",
			agentPort: 8088,
			wantErr:   true,
		},
		{
			name:      "unsupported scheme",
			url:       "file://localhost:8088/tmp",
			agentPort: 8088,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateAgentProxyURL(tt.url, tt.agentPort)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateAgentProxyURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateExternalBrowserURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name: "http",
			url:  "http://example.com/path",
		},
		{
			name: "https",
			url:  "https://example.com/path",
		},
		{
			name:    "file scheme",
			url:     "file:///C:/temp/file.txt",
			wantErr: true,
		},
		{
			name:    "custom uri handler",
			url:     "vscode://file/C:/temp/file.txt",
			wantErr: true,
		},
		{
			name:    "userinfo",
			url:     "https://user:" + "pass@example.com/path",
			wantErr: true,
		},
		{
			name:    "missing host",
			url:     "https:///path",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateExternalBrowserURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateExternalBrowserURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInspectorOriginValidation(t *testing.T) {
	port := 8087

	tests := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{
			name:   "localhost",
			host:   "localhost:8087",
			origin: "http://localhost:8087",
			want:   true,
		},
		{
			name:   "ipv4",
			host:   "127.0.0.1:8087",
			origin: "http://127.0.0.1:8087",
			want:   true,
		},
		{
			name:   "ipv6",
			host:   "[::1]:8087",
			origin: "http://[::1]:8087",
			want:   true,
		},
		{
			name:   "rebinding host",
			host:   "evil.example:8087",
			origin: "http://evil.example:8087",
		},
		{
			name:   "rebinding origin",
			host:   "127.0.0.1:8087",
			origin: "http://evil.example:8087",
		},
		{
			name:   "wrong port",
			host:   "localhost:8087",
			origin: "http://localhost:8088",
		},
		{
			name:   "origin with path",
			host:   "localhost:8087",
			origin: "http://localhost:8087/path",
		},
		{
			name: "empty origin",
			host: "localhost:8087",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllowedInspectorHostPort(tt.host, port) &&
				tt.origin != "" &&
				isAllowedInspectorOrigin(tt.origin, port)
			if got != tt.want {
				t.Fatalf("origin validation = %v, want %v", got, tt.want)
			}
		})
	}
}
