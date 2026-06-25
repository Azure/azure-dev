// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func (f *fakeUnmarshaler) Unmarshal([]byte) error { return nil }

func (f *failingMarshaler) Marshal() ([]byte, error) {
	return nil, errors.New("marshal-fail")
}

// fakeJWT builds a minimal JWT whose payload encodes the given claims.
func fakeJWT(t *testing.T, claims TokenClaims) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)

	header := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"alg":"none","typ":"JWT"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + body + "." + sig
}
