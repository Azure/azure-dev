// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package exterrors

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNonDisclosureErrorRedactsURLCredentialsContainingApostrophe(t *testing.T) {
	err := nonDisclosureError(errors.New(
		"request https://user:pa'ss@example.test/download?sig=secret failed",
	))

	require.Error(t, err)
	require.NotContains(t, err.Error(), "user")
	require.NotContains(t, err.Error(), "pa'ss")
	require.NotContains(t, err.Error(), "ss@example")
	require.NotContains(t, err.Error(), "sig=")
	require.NotContains(t, err.Error(), "secret")
	require.Contains(t, err.Error(), "https://example.test/download")
}

func TestNonDisclosureErrorRedactsWholeMalformedURLCandidate(t *testing.T) {
	err := nonDisclosureError(errors.New(
		"request https://user:password@example.test/%zz?sig=secret failed",
	))

	require.Error(t, err)
	require.NotContains(t, err.Error(), "password")
	require.NotContains(t, err.Error(), "sig=")
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), "example.test")
	require.Contains(t, err.Error(), "<redacted-url>")
}

func TestNonDisclosureErrorRedactsWrappedContextSentinelsWithoutRetainingChain(t *testing.T) {
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
		// #nosec G101 -- synthetic credential-bearing URL exercises redaction on wrapped context errors.
		wrapped := &url.Error{
			Op:  "Get",
			URL: "https://user:password@example.test/download?sig=secret#fragment",
			Err: sentinel,
		}
		err := NonDisclosureError(wrapped)
		require.Error(t, err)
		require.ErrorIs(t, err, sentinel)
		_, hasURLErr := errors.AsType[*url.Error](err)
		require.False(t, hasURLErr, "original *url.Error must not remain unwrappable")
		for _, leaked := range []string{"user", "password", "sig=", "secret", "fragment"} {
			require.NotContains(t, err.Error(), leaked)
		}
		require.Contains(t, err.Error(), "https://example.test/download")
	}
}
