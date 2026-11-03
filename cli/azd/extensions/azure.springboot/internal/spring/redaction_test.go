// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package spring

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNonDisclosureErrorRedactsURLCredentials(t *testing.T) {
	err := nonDisclosureError(errors.New(
		"request https://user:password@example.test/download?sig=secret&token=value#fragment failed",
	))

	require.Error(t, err)
	require.NotContains(t, err.Error(), "user")
	require.NotContains(t, err.Error(), "password")
	require.NotContains(t, err.Error(), "sig=")
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), "token=")
	require.NotContains(t, err.Error(), "fragment")
	require.Contains(t, err.Error(), "https://example.test/download")
}

func TestNonDisclosureErrorRedactsNonHTTPURLCredentials(t *testing.T) {
	err := nonDisclosureError(errors.New(
		"repository maven://user:password@example.test/releases?token=secret#fragment failed",
	))

	require.Error(t, err)
	require.NotContains(t, err.Error(), "user")
	require.NotContains(t, err.Error(), "password")
	require.NotContains(t, err.Error(), "token=")
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), "fragment")
	require.Contains(t, err.Error(), "maven://example.test/releases")
}
