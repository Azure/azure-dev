// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// ResilientClient is an HTTP client with built-in retry, exponential backoff,
// timeout, and optional bearer-token injection. It is designed for extension
// authors who need to call Azure REST APIs directly.
//
// Usage:
//
//	rc := azdext.NewResilientClient(tokenProvider, nil)
//	resp, err := rc.Do(ctx, http.MethodGet, "https://management.azure.com/...", nil)
type ResilientClient struct {
	httpClient    *http.Client
	tokenProvider azcore.TokenCredential
	scopeDetector *ScopeDetector
	opts          ResilientClientOptions
}

// ResilientClientOptions configures a [ResilientClient].
type ResilientClientOptions struct {
	// MaxRetries is the maximum number of retry attempts for transient failures.
	// Defaults to 3.
	MaxRetries int

	// InitialDelay is the base delay before the first retry. Subsequent retries
	// use exponential backoff (delay * 2^attempt) capped at MaxDelay.
	// Defaults to 500ms.
	InitialDelay time.Duration

	// MaxDelay caps the computed backoff delay. Defaults to 30s.
	MaxDelay time.Duration

	// Timeout is the per-request timeout. Defaults to 30s.
	// A zero value means no timeout (not recommended).
	Timeout time.Duration

	// Transport overrides the default HTTP transport. Useful for testing.
	Transport http.RoundTripper

	// ScopeDetector overrides the default scope detector used for automatic
	// scope resolution. When nil, a default detector is created.
	ScopeDetector *ScopeDetector
}

// defaults fills zero-value fields with production defaults.
func (o *ResilientClientOptions) defaults() {
	if o.MaxRetries <= 0 {
		o.MaxRetries = 3
	}

	if o.InitialDelay <= 0 {
		o.InitialDelay = 500 * time.Millisecond
	}

	if o.MaxDelay <= 0 {
		o.MaxDelay = 30 * time.Second
	}

	if o.Timeout <= 0 {
		o.Timeout = 30 * time.Second
	}
}

// NewResilientClient creates a [ResilientClient].
//
// tokenProvider may be nil if the caller handles Authorization headers manually.
// When non-nil, the client automatically injects a Bearer token using scopes
// resolved from the request URL via the [ScopeDetector].
func NewResilientClient(tokenProvider azcore.TokenCredential, opts *ResilientClientOptions) *ResilientClient {
	if opts == nil {
		opts = &ResilientClientOptions{}
	}

	opts.defaults()

	transport := opts.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}

	sd := opts.ScopeDetector
	if sd == nil {
		sd = NewScopeDetector(nil)
	}

	return &ResilientClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   opts.Timeout,
		},
		tokenProvider: tokenProvider,
		scopeDetector: sd,
		opts:          *opts,
	}
}

// Do executes an HTTP request with retry logic and optional bearer-token injection.
//
// body may be nil for requests without a body (GET, DELETE).
// The body must support seeking (implement io.ReadSeeker) when retries are enabled,
// so it can be re-read on each attempt. If body does not implement io.ReadSeeker
// and a retry is needed, the retry will proceed with a nil body.
func (rc *ResilientClient) Do(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	if ctx == nil {
		return nil, errors.New("azdext.ResilientClient.Do: context must not be nil")
	}

	var lastErr error

	for attempt := range rc.opts.MaxRetries + 1 {
		if attempt > 0 {
			delay := rc.backoff(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}

			// Reset body for retry if possible.
			if seeker, ok := body.(io.ReadSeeker); ok {
				if _, err := seeker.Seek(0, io.SeekStart); err != nil {
					return nil, fmt.Errorf("azdext.ResilientClient.Do: failed to reset request body: %w", err)
				}
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return nil, fmt.Errorf("azdext.ResilientClient.Do: failed to create request: %w", err)
		}

		// Inject bearer token when a token provider is available.
		if rc.tokenProvider != nil {
			if authErr := rc.applyAuth(ctx, req); authErr != nil {
				return nil, fmt.Errorf("azdext.ResilientClient.Do: authorization failed: %w", authErr)
			}
		}

		resp, err := rc.httpClient.Do(req)
		if err != nil {
			lastErr = err

			// Don't retry on context cancellation.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			continue // network error → retry
		}

		if !isRetryable(resp.StatusCode) {
			return resp, nil
		}

		// Consume body before retry to release the connection.
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		// Honor Retry-After if present.
		if ra := retryAfterFromResponse(resp); ra > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(ra):
			}
		}

		lastErr = &RetryableHTTPError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
		}
	}

	return nil, fmt.Errorf("azdext.ResilientClient.Do: exhausted retries: %w", lastErr)
}

// applyAuth resolves scopes from the request URL and sets the Authorization header.
func (rc *ResilientClient) applyAuth(ctx context.Context, req *http.Request) error {
	scopes, err := rc.scopeDetector.ScopesForURL(req.URL.String())
	if err != nil {
		return err
	}

	tok, err := rc.tokenProvider.GetToken(ctx, policy.TokenRequestOptions{Scopes: scopes})
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+tok.Token)

	return nil
}

// backoff computes the delay for a given attempt using exponential backoff.
func (rc *ResilientClient) backoff(attempt int) time.Duration {
	delay := time.Duration(float64(rc.opts.InitialDelay) * math.Pow(2, float64(attempt-1)))
	if delay > rc.opts.MaxDelay {
		delay = rc.opts.MaxDelay
	}

	return delay
}

// isRetryable returns true for status codes that indicate a transient failure.
func isRetryable(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// retryAfterFromResponse extracts the Retry-After duration from response headers.
// Checks: retry-after-ms, x-ms-retry-after-ms, retry-after (seconds or HTTP-date).
func retryAfterFromResponse(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}

	type retryHeader struct {
		header string
		units  time.Duration
		custom func(string) time.Duration
	}

	nop := func(string) time.Duration { return 0 }

	headers := []retryHeader{
		{header: "retry-after-ms", units: time.Millisecond, custom: nop},
		{header: "x-ms-retry-after-ms", units: time.Millisecond, custom: nop},
		{header: "retry-after", units: time.Second, custom: func(v string) time.Duration {
			t, err := time.Parse(time.RFC1123, v)
			if err != nil {
				return 0
			}
			return time.Until(t)
		}},
	}

	for _, rh := range headers {
		v := resp.Header.Get(rh.header)
		if v == "" {
			continue
		}

		if n, _ := strconv.Atoi(v); n > 0 {
			return time.Duration(n) * rh.units
		}

		if d := rh.custom(v); d > 0 {
			return d
		}
	}

	return 0
}

// RetryableHTTPError represents a retryable HTTP failure.
type RetryableHTTPError struct {
	StatusCode int
	Status     string
}

func (e *RetryableHTTPError) Error() string {
	return fmt.Sprintf("azdext.ResilientClient: retryable HTTP error %d (%s)", e.StatusCode, e.Status)
}
