// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"regexp"
	"strings"
	"testing"
)

func TestContainerAppName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "my_app", want: "my-app"},
		{name: "my_app_name", want: "my-app-name"},
		{name: "MY_APP", want: "my-app"},
		{name: "my__app", want: "my-app"},
		{name: "my_-app", want: "my-app"},
		{name: "my-_app", want: "my-app"},
		{name: "my_--__app", want: "my-app"},
		{name: "_my_app_", want: "my-app"},
		{name: "_--my_app--_", want: "my-app"},
		{name: "my-app", want: "my-app"},
		{name: "my--app", want: "my-app"},
		{name: "--my-app--", want: "my-app"},
		{name: "!_MY_APP_!", want: "-my-app-"},
		{name: "my_!_APP", want: "my--app"},
		{name: "MY.\u00e9APP123", want: "myapp123"},
		{name: "_--_", want: ""},
		{name: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContainerAppName(tt.name); got != tt.want {
				t.Fatalf("ContainerAppName(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestContainerAppNameTruncation(t *testing.T) {
	tests := []struct {
		name   string
		maxLen int
		want   string
	}{
		{name: "MY___APP", maxLen: 6, want: "my-a"},
		{name: "MY_APP", maxLen: 3, want: "my"},
		{name: "!!MY_APP", maxLen: 5, want: "my"},
		{name: "\u00e9MY_APP", maxLen: 3, want: "m"},
		{name: "\u00e9MY_APP", maxLen: 1, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerAppName(tt.name, tt.maxLen); got != tt.want {
				t.Fatalf("containerAppName(%q, %d) = %q, want %q", tt.name, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestSecretName(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)
	seen := map[string]string{}
	for _, name := range []string{"TOKEN", "TOKEN_", "_", "TOKEN__", strings.Repeat("X", 300)} {
		secret := SecretName(name)
		if !valid.MatchString(secret) || len(secret) > 253 {
			t.Fatalf("invalid secret identifier for %q: %q", name, secret)
		}
		if previous, ok := seen[secret]; ok {
			t.Fatalf("secret identifier collision between %q and %q", name, previous)
		}
		seen[secret] = name
		if secret != SecretName(name) {
			t.Fatal("secret identifiers must be deterministic")
		}
	}
}
