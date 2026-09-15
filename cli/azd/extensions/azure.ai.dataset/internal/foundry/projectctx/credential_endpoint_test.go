// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package projectctx

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pastedSAS = "sv=2021-08-06&sig=s0m3-l1v3-s1gnatur3"

// A project endpoint is scheme, host and path. Normalizing a credential away
// would accept the paste in silence and then use a different endpoint than the
// one the caller believes they gave -- and leave that value free to be logged
// or echoed on the way there.
func TestAnEndpointCarryingACredentialIsRefused(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"https://acct.services.ai.azure.com/api/projects/p?" + pastedSAS,
		"https://user:password@acct.services.ai.azure.com/api/projects/p",
		"https://acct.services.ai.azure.com/api/projects/p#" + pastedSAS,
	} {
		_, _, err := Validate(raw)

		require.Errorf(t, err, "should refuse %q", raw)
		assert.NotContains(t, err.Error(), "s0m3-l1v3-s1gnatur3",
			"the refusal must not repeat the credential back")
		assert.NotContains(t, err.Error(), "password")
	}
}

// The ordinary endpoint is unaffected, including the trailing slash and the
// mixed case the normalizer already handled.
func TestAnOrdinaryEndpointStillValidates(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"https://acct.services.ai.azure.com/api/projects/p":   "https://acct.services.ai.azure.com/api/projects/p",
		"https://acct.services.ai.azure.com/api/projects/p/":  "https://acct.services.ai.azure.com/api/projects/p",
		"https://ACCT.services.ai.azure.com/api/projects/p":   "https://acct.services.ai.azure.com/api/projects/p",
		" https://acct.services.ai.azure.com/api/projects/p ": "https://acct.services.ai.azure.com/api/projects/p",
	} {
		got, _, err := Validate(raw)
		require.NoErrorf(t, err, "should accept %q", raw)
		assert.Equal(t, want, got)
	}
}

// url.Parse returns a *url.Error carrying the URL it could not read, so
// reporting it whole printed whatever the caller pasted -- including the query
// of a SAS URL, which is the one thing that must not reach a terminal or a log.
func TestAnUnparseableEndpointDoesNotEchoWhatWasPasted(t *testing.T) {
	t.Parallel()

	// A control character is what url.Parse refuses outright, so the error it
	// returns is the one that carries the whole string.
	_, _, err := Validate("https://acct.services.ai.azure.com/\x7f?" + pastedSAS)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s0m3-l1v3-s1gnatur3",
		"the signature must not survive into the message")
	assert.NotContains(t, strings.ToLower(err.Error()), "sig=")
}
