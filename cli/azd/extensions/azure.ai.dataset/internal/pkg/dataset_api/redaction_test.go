// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package dataset_api

import (
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The credential in a storage SAS URI lives in the query string as `sig`.
// url.URL.Redacted only masks a userinfo password, so it is not a safe way to
// log one of these: it returns the signature verbatim. These tests pin the
// distinction, because the unsafe call is the one that looks correct.
func TestLogSafeURLDropsTheSASSignature(t *testing.T) {
	raw := "https://acct.blob.core.windows.net/c/rows.jsonl" +
		"?sv=2021-08-06&sig=REDACT_ME_SECRET&se=2030-01-01"
	u, err := url.Parse(raw)
	require.NoError(t, err)

	assert.Contains(t, u.Redacted(), "REDACT_ME_SECRET",
		"guards the premise: Redacted() alone leaks the signature")

	safe := logSafeURL(u)
	assert.NotContains(t, safe, "REDACT_ME_SECRET", "the SAS signature must never reach a log")
	assert.NotContains(t, safe, "sig=")
	assert.Equal(t, "https://acct.blob.core.windows.net/c/rows.jsonl", safe,
		"scheme, host and path stay, so the log is still useful")
	assert.Equal(t, raw, u.String(), "the caller's URL is left intact for the request itself")
}

func TestRedactURLErrorStripsTheSASFromClientErrors(t *testing.T) {
	inner := errors.New("dial tcp: lookup failed")
	original := &url.Error{
		Op:  "Put",
		URL: "https://acct.blob.core.windows.net/c/rows.jsonl?sig=REDACT_ME_SECRET",
		Err: inner,
	}

	got := redactURLError(original)

	assert.NotContains(t, got.Error(), "REDACT_ME_SECRET",
		"a transport failure must not surface the upload SAS to the user")
	assert.Contains(t, got.Error(), "acct.blob.core.windows.net",
		"the host is kept so the message still says where it failed")
	assert.ErrorIs(t, got, inner, "the cause stays unwrappable")
	assert.Contains(t, original.URL, "REDACT_ME_SECRET", "the original error is not mutated")
}

// Errors that are not *url.Error carry no URL, so they pass through untouched.
func TestRedactURLErrorLeavesOtherErrorsAlone(t *testing.T) {
	plain := errors.New("some other failure")
	assert.Same(t, plain, redactURLError(plain))
	assert.Nil(t, redactURLError(nil))
}
