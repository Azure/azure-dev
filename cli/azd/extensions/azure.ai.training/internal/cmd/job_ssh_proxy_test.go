// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestBuildTunnelWSURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "wss already, no trailing slash",
			input: "wss://ssh-abc.eastus2euap.nodes.azureml.ms",
			want:  "wss://ssh-abc.eastus2euap.nodes.azureml.ms/nbip/v1.0/ws-tcp",
		},
		{
			name:  "wss already, trailing slash trimmed",
			input: "wss://ssh-abc.eastus2euap.nodes.azureml.ms/",
			want:  "wss://ssh-abc.eastus2euap.nodes.azureml.ms/nbip/v1.0/ws-tcp",
		},
		{
			name:  "https rewritten to wss",
			input: "https://ssh-abc.eastus2euap.nodes.azureml.ms",
			want:  "wss://ssh-abc.eastus2euap.nodes.azureml.ms/nbip/v1.0/ws-tcp",
		},
		{
			name:  "http rewritten to ws",
			input: "http://localhost:8080",
			want:  "ws://localhost:8080/nbip/v1.0/ws-tcp",
		},
		{
			name:  "ws untouched",
			input: "ws://localhost:8080",
			want:  "ws://localhost:8080/nbip/v1.0/ws-tcp",
		},
		{
			name:  "unknown scheme passes through (regex layer rejects upstream)",
			input: "ftp://nope",
			want:  "ftp://nope/nbip/v1.0/ws-tcp",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildTunnelWSURL(tc.input)
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
