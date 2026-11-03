// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package scaffold

import (
	"regexp"
	"strings"
	"testing"
)

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
