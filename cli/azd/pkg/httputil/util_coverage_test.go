// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package httputil

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errReader is an io.Reader that always returns an error.
type errReader struct{}

func (e errReader) Read(_ []byte) (int, error) {
	return 0, errors.New("simulated read error")
}

// coveragePayload is a simple struct for generic deserialization tests.
type coveragePayload struct {
	Value string `json:"value"`
}

func TestReadRawResponse_InvalidJSON(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body: io.NopCloser(
			bytes.NewBufferString("not valid json"),
		),
	}

	result, err := ReadRawResponse[coveragePayload](resp)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed unmarshalling JSON")
}

func TestReadRawResponse_ReadError(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(errReader{}),
	}

	result, err := ReadRawResponse[coveragePayload](resp)
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "simulated read error")
}

func TestReadRawResponse_EmptyBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewBufferString("")),
	}

	result, err := ReadRawResponse[coveragePayload](resp)
	require.Error(t, err)
	assert.Nil(t, result)
}

func TestRetryAfter(t *testing.T) {
	tests := []struct {
		name     string
		resp     *http.Response
		want     time.Duration
		wantZero bool
	}{
		{
			name:     "NilResponse",
			resp:     nil,
			wantZero: true,
		},
		{
			name: "NoRetryHeaders",
			resp: &http.Response{
				Header: http.Header{
					"Content-Type": {"application/json"},
				},
			},
			wantZero: true,
		},
		{
			name: "EmptyHeaders",
			resp: &http.Response{
				Header: http.Header{},
			},
			wantZero: true,
		},
		{
			name: "RetryAfterMs",
			resp: &http.Response{
				Header: http.Header{
					"Retry-After-Ms": {"150"},
				},
			},
			want: 150 * time.Millisecond,
		},
		{
			name: "XMsRetryAfterMs",
			resp: &http.Response{
				Header: http.Header{
					"X-Ms-Retry-After-Ms": {"250"},
				},
			},
			want: 250 * time.Millisecond,
		},
		{
			name: "RetryAfterSeconds",
			resp: &http.Response{
				Header: http.Header{
					"Retry-After": {"3"},
				},
			},
			want: 3 * time.Second,
		},
		{
			name: "RetryAfterMsHasHighestPrecedence",
			resp: &http.Response{
				Header: http.Header{
					"Retry-After-Ms":      {"100"},
					"X-Ms-Retry-After-Ms": {"200"},
					"Retry-After":         {"5"},
				},
			},
			want: 100 * time.Millisecond,
		},
		{
			name: "XMsRetryAfterMsPrecedesRetryAfter",
			resp: &http.Response{
				Header: http.Header{
					"X-Ms-Retry-After-Ms": {"300"},
					"Retry-After":         {"10"},
				},
			},
			want: 300 * time.Millisecond,
		},
		{
			name: "InvalidNonNumericRetryAfter",
			resp: &http.Response{
				Header: http.Header{
					"Retry-After": {"not-a-number"},
				},
			},
			wantZero: true,
		},
		{
			name: "ZeroValueRetryAfter",
			resp: &http.Response{
				Header: http.Header{
					"Retry-After": {"0"},
				},
			},
			wantZero: true,
		},
		{
			name: "NegativeRetryAfter",
			resp: &http.Response{
				Header: http.Header{
					"Retry-After": {"-5"},
				},
			},
			wantZero: true,
		},
		{
			name: "ZeroValueRetryAfterMs",
			resp: &http.Response{
				Header: http.Header{
					"Retry-After-Ms": {"0"},
				},
			},
			wantZero: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := RetryAfter(tt.resp)
			if tt.wantZero {
				assert.Equal(t, time.Duration(0), d)
				return
			}
			assert.Equal(t, tt.want, d)
		})
	}
}

func TestRetryAfter_RFC1123DateFormat(t *testing.T) {
	futureTime := time.Now().Add(30 * time.Second)
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": {
				futureTime.UTC().Format(time.RFC1123),
			},
		},
	}

	d := RetryAfter(resp)
	// Should be close to 30s with some tolerance for test execution
	assert.Greater(t, d, 25*time.Second)
	assert.Less(t, d, 35*time.Second)
}

func TestRetryAfter_PastDateReturnsZero(t *testing.T) {
	pastTime := time.Now().Add(-60 * time.Second)
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": {
				pastTime.UTC().Format(time.RFC1123),
			},
		},
	}

	d := RetryAfter(resp)
	assert.Equal(t, time.Duration(0), d)
}

func TestRetryAfter_InvalidDateFormat(t *testing.T) {
	resp := &http.Response{
		Header: http.Header{
			"Retry-After": {"Mon, 99 Abc 9999 99:99:99 ZZZ"},
		},
	}

	d := RetryAfter(resp)
	assert.Equal(t, time.Duration(0), d)
}

func TestTlsEnabledTransport_InvalidBase64(t *testing.T) {
	transport, err := TlsEnabledTransport("not-valid-base64!!!")
	require.Error(t, err)
	assert.Nil(t, transport)
	assert.Contains(t, err.Error(), "failed to decode")
}

func TestTlsEnabledTransport_InvalidCertBytes(t *testing.T) {
	// Valid base64 encoding of "hello world" — not a real DER cert
	validBase64InvalidCert := "aGVsbG8gd29ybGQ="
	transport, err := TlsEnabledTransport(validBase64InvalidCert)
	require.Error(t, err)
	assert.Nil(t, transport)
	assert.Contains(t, err.Error(), "failed to parse")
}
