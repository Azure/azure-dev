// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package urlsafe

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sasSecret = "REDACT_ME_SECRET"

// These tests pin the premise as well as the behavior: url.URL.Redacted is the
// call that looks correct and leaks, so if someone reaches for it again the
// first assertion explains why they should not.
func TestURLDropsTheSASSignature(t *testing.T) {
	raw := "https://acct.blob.core.windows.net/c/rows.jsonl?sv=2021-08-06&sig=" + sasSecret
	u, err := url.Parse(raw)
	require.NoError(t, err)

	assert.Contains(t, u.Redacted(), sasSecret,
		"guards the premise: Redacted() alone leaks the signature")

	safe := URL(u)
	assert.NotContains(t, safe, sasSecret, "the SAS signature must never reach a log")
	assert.NotContains(t, safe, "sig=")
	assert.Equal(t, "https://acct.blob.core.windows.net/c/rows.jsonl", safe,
		"scheme, host and path stay, so the log still says where the request went")
	assert.Equal(t, raw, u.String(), "the caller's URL is untouched and still usable")
}

func TestURLHandlesNil(t *testing.T) {
	assert.Equal(t, "", URL(nil))
}

// Redacted() masks a userinfo password and prints the username verbatim, so a
// credential passed either way has to be dropped here rather than rendered.
func TestURLDropsUserinfoCredentials(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"password", "https://user:" + sasSecret + "@acct.blob.core.windows.net/c/rows.jsonl"},
		{"token as username", "https://" + sasSecret + "@acct.blob.core.windows.net/c/rows.jsonl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			require.NoError(t, err)

			safe := URL(u)
			assert.NotContains(t, safe, sasSecret, "userinfo must never reach a log")
			assert.NotContains(t, safe, "@")
			assert.Equal(t, "https://acct.blob.core.windows.net/c/rows.jsonl", safe)
			assert.Equal(t, tc.raw, u.String(), "the caller's URL is untouched")
		})
	}

	u, err := url.Parse("https://" + sasSecret + "@acct.blob.core.windows.net/c/rows.jsonl")
	require.NoError(t, err)
	assert.Contains(t, u.Redacted(), sasSecret,
		"guards the premise: Redacted() alone leaks a username-only token")
}

// The transport error carries the request URL, so userinfo has to be dropped on
// that path too and not only on the query.
func TestErrorStripsUserinfoFromTransportFailures(t *testing.T) {
	original := &url.Error{
		Op:  "Get",
		URL: "https://" + sasSecret + "@acct.blob.core.windows.net/c/rows.jsonl",
		Err: errors.New("dial tcp: lookup failed"),
	}

	got := Error(original)

	assert.NotContains(t, got.Error(), sasSecret)
	assert.Contains(t, got.Error(), "acct.blob.core.windows.net")
}

func TestErrorStripsTheSASFromTransportFailures(t *testing.T) {
	inner := errors.New("dial tcp: lookup failed")
	original := &url.Error{
		Op:  "Get",
		URL: "https://acct.blob.core.windows.net/c/rows.jsonl?sig=" + sasSecret,
		Err: inner,
	}

	got := Error(original)

	assert.NotContains(t, got.Error(), sasSecret,
		"a transport failure must not show the SAS to the user")
	assert.Contains(t, got.Error(), "acct.blob.core.windows.net",
		"the host stays so the message still says where it failed")
	assert.ErrorIs(t, got, inner, "the cause stays unwrappable")
	assert.Contains(t, original.URL, sasSecret, "the original error is not mutated")
}

func TestErrorLeavesOtherErrorsAlone(t *testing.T) {
	plain := errors.New("some other failure")
	assert.Same(t, plain, Error(plain))
	assert.Nil(t, Error(nil))
}
