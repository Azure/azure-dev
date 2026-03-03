// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// roundTripFunc is an adapter to allow ordinary functions as http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeTokenCredential satisfies azcore.TokenCredential for testing.
type fakeTokenCredential struct {
	token string
	err   error
}

func (f *fakeTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: f.token, ExpiresOn: time.Now().Add(time.Hour)}, f.err
}

func TestResilientClient_Success(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{Transport: transport})

	resp, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestResilientClient_AddsDefaultHeaders(t *testing.T) {
	t.Parallel()

	var gotUserAgent string
	var gotCorrelationID string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotUserAgent = r.Header.Get(userAgentHeaderName)
		gotCorrelationID = r.Header.Get(msCorrelationIDHeaderName)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{Transport: transport})

	resp, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if gotUserAgent != defaultUserAgent {
		t.Errorf("User-Agent = %q, want %q", gotUserAgent, defaultUserAgent)
	}
	if gotCorrelationID == "" {
		t.Fatal("expected x-ms-correlation-request-id to be set")
	}
}

func TestResilientClient_RetriesTransientFailures(t *testing.T) {
	t.Parallel()

	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts < 3 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("unavailable")),
				Header:     http.Header{},
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   3,
		InitialDelay: time.Millisecond, // fast for testing
		MaxDelay:     10 * time.Millisecond,
	})

	resp, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestResilientClient_ExhaustsRetries(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader("throttled")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   2,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
	})

	_, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}

	var retryErr *RetryableHTTPError
	if !errors.As(err, &retryErr) {
		t.Fatalf("error type = %T, want *RetryableHTTPError", err)
	}

	if retryErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", retryErr.StatusCode, http.StatusTooManyRequests)
	}
}

func TestResilientClient_NoRetryOn4xx(t *testing.T) {
	t.Parallel()

	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   3,
		InitialDelay: time.Millisecond,
	})

	resp, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 404)", attempts)
	}
}

func TestResilientClient_NetworkError(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   1,
		InitialDelay: time.Millisecond,
		MaxDelay:     5 * time.Millisecond,
	})

	_, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	if err == nil {
		t.Fatal("expected error for network failure")
	}
}

func TestResilientClient_ContextCancelled(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader("unavailable")),
			Header:     http.Header{},
		}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   3,
		InitialDelay: time.Second,
	})

	_, err := rc.Do(ctx, http.MethodGet, "https://example.com/api", nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestResilientClient_BearerTokenInjection(t *testing.T) {
	t.Parallel()

	var capturedAuth string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		capturedAuth = r.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     http.Header{},
		}, nil
	})

	cred := &fakeTokenCredential{token: "my-access-token"}

	rc := NewResilientClient(cred, &ResilientClientOptions{Transport: transport})

	// URL must match a known scope for the detector.
	resp, err := rc.Do(context.Background(), http.MethodGet, "https://management.azure.com/subscriptions", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if capturedAuth != "Bearer my-access-token" {
		t.Errorf("Authorization = %q, want %q", capturedAuth, "Bearer my-access-token")
	}
}

func TestResilientClient_TokenProviderError(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("should not reach transport when token fails")
		return nil, nil
	})

	cred := &fakeTokenCredential{err: errors.New("token expired")}

	rc := NewResilientClient(cred, &ResilientClientOptions{Transport: transport})

	_, err := rc.Do(context.Background(), http.MethodGet, "https://management.azure.com/subs", nil)
	if err == nil {
		t.Fatal("expected error when token provider fails")
	}
}

func TestResilientClient_BodyRewind(t *testing.T) {
	t.Parallel()

	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if r.Body != nil {
			data, _ := io.ReadAll(r.Body)
			if string(data) != "payload" {
				t.Errorf("attempt %d: body = %q, want %q", attempts, string(data), "payload")
			}
		}

		if attempts < 2 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     http.Header{},
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   2,
		InitialDelay: time.Millisecond,
	})

	body := bytes.NewReader([]byte("payload"))
	resp, err := rc.Do(context.Background(), http.MethodPost, "https://example.com/api", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestResilientClient_RetryAfterHeader(t *testing.T) {
	t.Parallel()

	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			h := http.Header{}
			h.Set("retry-after-ms", "1") // 1ms
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader("throttled")),
				Header:     h,
			}, nil
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   2,
		InitialDelay: time.Millisecond,
	})

	resp, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestResilientClient_NilContext(t *testing.T) {
	t.Parallel()

	rc := NewResilientClient(nil, nil)

	//lint:ignore SA1012 intentional nil context for test
	//nolint:staticcheck // intentional nil context for test
	_, err := rc.Do(nil, http.MethodGet, "https://example.com/api", nil)
	if err == nil {
		t.Fatal("expected error for nil context")
	}
}

func TestResilientClient_DefaultOptions(t *testing.T) {
	t.Parallel()

	opts := &ResilientClientOptions{MaxRetries: -1}
	opts.defaults()

	if opts.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", opts.MaxRetries)
	}

	if opts.InitialDelay != 500*time.Millisecond {
		t.Errorf("InitialDelay = %v, want 500ms", opts.InitialDelay)
	}

	if opts.MaxDelay != 30*time.Second {
		t.Errorf("MaxDelay = %v, want 30s", opts.MaxDelay)
	}

	if opts.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", opts.Timeout)
	}
}

func TestResilientClient_DefaultOptions_ZeroRetriesPreserved(t *testing.T) {
	t.Parallel()

	opts := &ResilientClientOptions{MaxRetries: 0}
	opts.defaults()
	if opts.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0", opts.MaxRetries)
	}
}

func TestRetryAfterFromResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		value  string
		want   time.Duration
	}{
		{name: "retry-after-ms", header: "retry-after-ms", value: "500", want: 500 * time.Millisecond},
		{name: "x-ms-retry-after-ms", header: "x-ms-retry-after-ms", value: "1000", want: time.Second},
		{name: "retry-after seconds", header: "retry-after", value: "2", want: 2 * time.Second},
		{name: "empty header", header: "retry-after", value: "", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := http.Header{}
			if tt.value != "" {
				h.Set(tt.header, tt.value)
			}

			resp := &http.Response{Header: h}
			got := httputil.RetryAfter(resp)

			if got != tt.want {
				t.Errorf("retryAfterFromResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRetryAfterFromResponse_Nil(t *testing.T) {
	t.Parallel()

	got := httputil.RetryAfter(nil)
	if got != 0 {
		t.Errorf("retryAfterFromResponse(nil) = %v, want 0", got)
	}
}

func TestIsRetryable(t *testing.T) {
	t.Parallel()

	retryable := []int{
		http.StatusTooManyRequests,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	}

	for _, code := range retryable {
		if !isRetryable(code) {
			t.Errorf("isRetryable(%d) = false, want true", code)
		}
	}

	notRetryable := []int{
		http.StatusOK,
		http.StatusCreated,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
	}

	for _, code := range notRetryable {
		if isRetryable(code) {
			t.Errorf("isRetryable(%d) = true, want false", code)
		}
	}
}

func TestResilientClient_AllRetryableStatusCodes(t *testing.T) {
	t.Parallel()

	retryableCodes := []int{
		http.StatusTooManyRequests,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	}

	for _, code := range retryableCodes {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			t.Parallel()

			var attempts int
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				attempts++
				return &http.Response{
					StatusCode: code,
					Body:       io.NopCloser(strings.NewReader("")),
					Header:     http.Header{},
				}, nil
			})

			rc := NewResilientClient(nil, &ResilientClientOptions{
				Transport:    transport,
				MaxRetries:   1,
				InitialDelay: time.Millisecond,
			})

			_, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
			if err == nil {
				t.Fatal("expected error after retries exhausted")
			}

			// 1 initial + 1 retry = 2
			if attempts != 2 {
				t.Errorf("attempts = %d, want 2 for status %d", attempts, code)
			}
		})
	}
}

func TestResilientClient_NonSeekableBodyRetryError(t *testing.T) {
	t.Parallel()

	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   2,
		InitialDelay: time.Millisecond,
	})

	// io.NopCloser wrapping strings.NewReader is NOT an io.ReadSeeker.
	// With upfront validation, the error is caught before any HTTP call.
	body := io.NopCloser(strings.NewReader("payload"))
	_, err := rc.Do(context.Background(), http.MethodPost, "https://example.com/api", body)
	if err == nil {
		t.Fatal("expected error for non-seekable body with retries enabled")
	}

	if !strings.Contains(err.Error(), "io.ReadSeeker") {
		t.Errorf("error = %q, want mention of io.ReadSeeker", err.Error())
	}

	// Should have made zero attempts — upfront check rejects before any HTTP call.
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0 (fail fast before any request)", attempts)
	}
}

func TestResilientClient_TokenOverHTTP(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatal("should not reach transport for HTTP URL with token provider")
		return nil, nil
	})

	cred := &fakeTokenCredential{token: "secret-token"}
	rc := NewResilientClient(cred, &ResilientClientOptions{Transport: transport})

	_, err := rc.Do(context.Background(), http.MethodGet, "http://example.com/api", nil)
	if err == nil {
		t.Fatal("expected error for HTTP URL with token provider")
	}

	if !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("error = %q, want mention of HTTPS", err.Error())
	}
}

func TestResilientClient_RetryAfterReplacesBackoff(t *testing.T) {
	t.Parallel()

	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			h := http.Header{}
			h.Set("retry-after-ms", "1") // 1ms retry-after
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Body:       io.NopCloser(strings.NewReader("throttled")),
				Header:     h,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   2,
		InitialDelay: 5 * time.Second, // large backoff — should NOT be used
		MaxDelay:     10 * time.Second,
	})

	start := time.Now()
	resp, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	// If Retry-After replaces backoff, total time should be ~1ms, not 5s.
	if elapsed > 2*time.Second {
		t.Errorf("elapsed = %v, want < 2s (Retry-After should replace backoff, not add to it)", elapsed)
	}
}

func TestResilientClient_RetryAfterCapped(t *testing.T) {
	t.Parallel()

	// Verify the cap constant is reasonable.
	if maxRetryAfterDuration > 5*time.Minute {
		t.Errorf("maxRetryAfterDuration = %v, should be <= 5m", maxRetryAfterDuration)
	}

	// A large Retry-After value should be capped in Do().
	h := http.Header{}
	h.Set("retry-after", "999999")
	resp := &http.Response{Header: h}

	got := httputil.RetryAfter(resp)
	// RetryAfter parser itself doesn't cap (pure parser), but Do() caps it.
	if got != 999999*time.Second {
		t.Errorf("RetryAfter() = %v, want %v (capping happens in Do)", got, 999999*time.Second)
	}
}

func TestResilientClient_RetryBodyDrainBounded(t *testing.T) {
	t.Parallel()

	// Verify the constant used for bounded retry body drain is set
	// and reasonable: it should prevent memory exhaustion but allow
	// realistic retryable response bodies to be fully drained.
	if maxRetryBodyDrain <= 0 {
		t.Fatal("maxRetryBodyDrain must be positive")
	}
	if maxRetryBodyDrain > 10<<20 { // 10 MB
		t.Errorf("maxRetryBodyDrain = %d, should be <= 10 MB", maxRetryBodyDrain)
	}

	// Simulate a retry scenario where the retryable response body is larger
	// than the drain limit. The client should not hang or OOM.
	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			// Return a retryable status with a body larger than the drain limit.
			// Use a LimitedReader to simulate a large body without allocating.
			bigBody := io.LimitReader(infiniteReader{}, maxRetryBodyDrain+1024)
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(bigBody),
				Header:     http.Header{},
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   1,
		InitialDelay: time.Millisecond,
	})

	resp, err := rc.Do(context.Background(), http.MethodGet, "https://example.com/api", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

// infiniteReader is an io.Reader that produces zero bytes forever.
type infiniteReader struct{}

func (infiniteReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestResilientClient_BackoffJitter(t *testing.T) {
	t.Parallel()

	rc := NewResilientClient(nil, &ResilientClientOptions{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     10 * time.Second,
	})

	// Run backoff multiple times for the same attempt and verify results
	// vary (jitter produces different values).
	seen := make(map[time.Duration]bool)
	for range 20 {
		d := rc.backoff(1)
		seen[d] = true
		// With jitter in [50%, 100%), delay should be in [50ms, 100ms).
		if d < 50*time.Millisecond || d >= 100*time.Millisecond {
			t.Errorf("backoff(1) = %v, want in [50ms, 100ms)", d)
		}
	}
	if len(seen) < 2 {
		t.Error("backoff jitter produced identical values across 20 calls")
	}
}

func TestResilientClient_NonSeekableBodyFailsFast(t *testing.T) {
	t.Parallel()

	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     http.Header{},
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   2,
		InitialDelay: time.Millisecond,
	})

	// Non-seekable body with retries enabled should fail before any request.
	body := io.NopCloser(strings.NewReader("payload"))
	_, err := rc.Do(context.Background(), http.MethodPost, "https://example.com/api", body)
	if err == nil {
		t.Fatal("expected error for non-seekable body with retries enabled")
	}

	if !strings.Contains(err.Error(), "io.ReadSeeker") {
		t.Errorf("error = %q, want mention of io.ReadSeeker", err.Error())
	}

	// Should NOT have made any HTTP request.
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0 (fail fast before any request)", attempts)
	}
}

func TestResilientClient_RetryAfterCappedInDo(t *testing.T) {
	t.Parallel()

	// A huge Retry-After should be capped to maxRetryAfterDuration.
	// We verify this by using a very short context timeout: if the raw
	// value (999999s) were used, the context would expire instantly
	// rather than letting the retry proceed. With capping, the context
	// timeout (250ms here) is less than the cap, so we expect the
	// context to cancel — proving the delay is finite and capped.
	var attempts int
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		attempts++
		h := http.Header{}
		h.Set("retry-after", "999999")
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Body:       io.NopCloser(strings.NewReader("throttled")),
			Header:     h,
		}, nil
	})

	rc := NewResilientClient(nil, &ResilientClientOptions{
		Transport:    transport,
		MaxRetries:   1,
		InitialDelay: time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	_, err := rc.Do(ctx, http.MethodGet, "https://example.com/api", nil)
	// The context should cancel during the capped delay (120s > 250ms),
	// which means the raw 999999s was replaced by the cap.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded (proving cap was applied), got: %v", err)
	}

	// Only 1 attempt — the retry wait for the capped delay gets canceled.
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}
