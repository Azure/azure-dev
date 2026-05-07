// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package mocks

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// CreateJwtToken creates a JWT-like token for tests without using hardcoded token literals.
func CreateJwtToken(t testing.TB, claims any) string {
	t.Helper()

	header, err := json.Marshal(map[string]string{
		"alg": "none",
		"typ": "JWT",
	})
	if err != nil {
		t.Fatalf("marshaling JWT header: %v", err)
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshaling JWT claims: %v", err)
	}

	return strings.Join([]string{
		base64.RawURLEncoding.EncodeToString(header),
		base64.RawURLEncoding.EncodeToString(payload),
		"signature",
	}, ".")
}
