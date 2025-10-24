// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package recording

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTransport simulates network failures for testing
type mockTransport struct {
	failureCount int
	maxFailures  int
	errorMessage string
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.failureCount < m.maxFailures {
		m.failureCount++
		return nil, errors.New(m.errorMessage)
	}
	// After max failures, succeed
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}, nil
}

func TestIsNetworkError(t *testing.T) {
	tests := []struct {
		name          string
		error         error
		shouldBeRetry bool
	}{
		{
			name:          "nil error",
			error:         nil,
			shouldBeRetry: false,
		},
		{
			name:          "timeout error",
			error:         errors.New("request timeout"),
			shouldBeRetry: true,
		},
		{
			name:          "connection error",
			error:         errors.New("connection refused"),
			shouldBeRetry: true,
		},
		{
			name:          "dns error",
			error:         errors.New("no such host"),
			shouldBeRetry: true,
		},
		{
			name:          "i/o timeout error",
			error:         errors.New("i/o timeout"),
			shouldBeRetry: true,
		},
		{
			name:          "network unreachable error",
			error:         errors.New("network is unreachable"),
			shouldBeRetry: true,
		},
		{
			name:          "non-network error",
			error:         errors.New("invalid json"),
			shouldBeRetry: false,
		},
		{
			name:          "application logic error",
			error:         errors.New("user not found"),
			shouldBeRetry: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNetworkError(tt.error)
			assert.Equal(t, tt.shouldBeRetry, result)
		})
	}
}

func TestResilientHttpTransport_RetryOnNetworkErrors(t *testing.T) {
	tests := []struct {
		name         string
		errorMessage string
		maxFailures  int
		shouldRetry  bool
	}{
		{
			name:         "timeout error should retry",
			errorMessage: "request timeout",
			maxFailures:  2,
			shouldRetry:  true,
		},
		{
			name:         "connection error should retry",
			errorMessage: "connection refused",
			maxFailures:  1,
			shouldRetry:  true,
		},
		{
			name:         "dns error should retry",
			errorMessage: "no such host",
			maxFailures:  1,
			shouldRetry:  true,
		},
		{
			name:         "non-network error should not retry",
			errorMessage: "invalid json",
			maxFailures:  3,
			shouldRetry:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockTransport{
				maxFailures:  tt.maxFailures,
				errorMessage: tt.errorMessage,
			}

			resilient := NewResilientHttpTransport(mock)

			req, err := http.NewRequestWithContext(context.Background(), "GET", "http://example.com", nil)
			require.NoError(t, err)

			// Set a longer timeout to accommodate retries with exponential backoff
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			req = req.WithContext(ctx)

			resp, err := resilient.RoundTrip(req)

			if tt.shouldRetry && tt.maxFailures <= 3 { // Our max retries is 3
				// Should succeed after retries
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			} else {
				// Should fail without retrying or after max retries
				assert.Error(t, err)
				assert.Nil(t, resp)
				if tt.shouldRetry {
					// Error message should contain original error
					assert.True(t, strings.Contains(err.Error(), tt.errorMessage))
				}
			}
		})
	}
}

func TestResilientHttpTransport_ImmediateSuccess(t *testing.T) {
	// Test that successful requests go through without delay
	mock := &mockTransport{
		maxFailures: 0, // No failures
	}

	resilient := NewResilientHttpTransport(mock)

	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://example.com", nil)
	require.NoError(t, err)

	start := time.Now()
	resp, err := resilient.RoundTrip(req)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// Should complete quickly since there's no retry needed
	assert.Less(t, duration, 100*time.Millisecond)
}
