// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package recording

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/sethvargo/go-retry"
)

// networkErrorKeywords contains error message keywords that indicate network-related failures
// that should be retried
var networkErrorKeywords = []string{
	"timeout",
	"connection",
	"network",
	"dns",
	"tls handshake",
	"context deadline exceeded",
	"request timeout",
	"no such host",
	"connection refused",
	"connection reset",
	"i/o timeout",
	"network is unreachable",
	"temporary failure",
	"service unavailable",
}

// resilientHttpTransport wraps an HTTP transport with retry logic for network failures.
// This makes the test recorder more robust to transient network issues without affecting recorded interactions.
type resilientHttpTransport struct {
	transport http.RoundTripper
}

// NewResilientHttpTransport creates a new resilient HTTP transport that wraps the provided transport
func NewResilientHttpTransport(transport http.RoundTripper) *resilientHttpTransport {
	return &resilientHttpTransport{
		transport: transport,
	}
}

// isNetworkError checks if an error message contains keywords indicating a network-related failure
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	errStr := strings.ToLower(err.Error())
	for _, keyword := range networkErrorKeywords {
		if strings.Contains(errStr, keyword) {
			return true
		}
	}
	return false
}

// RoundTrip implements http.RoundTripper with retry logic for network failures
func (r *resilientHttpTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	// Retry logic with exponential backoff for network failures
	retryErr := retry.Do(
		req.Context(),
		retry.WithMaxRetries(3, retry.NewExponential(2*time.Second)),
		func(ctx context.Context) error {
			resp, err = r.transport.RoundTrip(req)
			if err != nil {
				// Check if error is likely network-related
				if isNetworkError(err) {
					return retry.RetryableError(err)
				}
				// For non-network errors, fail immediately
				return err
			}
			return nil
		},
	)

	if retryErr != nil {
		return nil, retryErr
	}
	return resp, nil
}
