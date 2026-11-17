// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
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
