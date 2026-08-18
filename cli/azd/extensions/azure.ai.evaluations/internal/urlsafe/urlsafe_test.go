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

// These tests pin the premise as well as the behaviour: url.URL.Redacted is the
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
