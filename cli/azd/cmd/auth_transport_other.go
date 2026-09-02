// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !unix && !windows

package cmd

import (
	"fmt"
	"net/http"
)

// newSocketTransport is not supported on this platform.
func newSocketTransport(rawURL string) (http.RoundTripper, string, error) {
	return nil, "", fmt.Errorf(
		"AZD_AUTH_ENDPOINT scheme 'unix' is not supported on this platform")
}

// newPipeTransport is not supported on this platform.
func newPipeTransport(rawURL string) (http.RoundTripper, string, error) {
	return nil, "", fmt.Errorf(
		"AZD_AUTH_ENDPOINT scheme 'npipe' is not supported on this platform")
}
