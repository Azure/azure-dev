// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInvokeLatencyFlag(t *testing.T) {
	t.Parallel()

	cmd := newInvokeCommand(nil)
	enabled, err := cmd.Flags().GetBool("debug-latency")
	require.NoError(t, err)
	require.True(t, enabled)

	require.NoError(t, cmd.ParseFlags([]string{"--debug-latency=false"}))
	enabled, err = cmd.Flags().GetBool("debug-latency")
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestNewInvokeLatency(t *testing.T) {
	t.Parallel()

	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "https://example.com/responses", nil)
			req.Header.Set("Authorization", "Bearer test-token")
			latency := newInvokeLatency(req, enabled, false)
			require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
			if enabled {
				require.NotNil(t, latency)
				require.Equal(t, "true", req.Header.Get(invokeLatencyHeaderPrefix+"enabled"))
			} else {
				require.Nil(t, latency)
				require.Empty(t, req.Header.Get(invokeLatencyHeaderPrefix+"enabled"))
				var output bytes.Buffer
				latency.captureResponse(&http.Response{StatusCode: http.StatusOK})
				require.NoError(t, latency.writeTo(&output))
				require.Empty(t, output.String())
			}
		})
	}
}

func TestInvokeLatencySummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		headers      map[string]string
		platformOnly bool
		want         string
	}{
		{
			name: "cold",
			headers: map[string]string{
				"session-start-type":        "cold",
				"platform-preprocessing-ms": "28",
				"infra-setup-ms":            "900",
				"container-readiness-ms":    "1700",
				"container-response-ms":     "372",
				"response-begin-ms":         "3000",
			},
			want: "Platform latency (cold): response headers 3000 ms\n" +
				"  preprocess 28 ms | infra 900 ms | readiness 1700 ms | container 372 ms\n",
		},
		{
			name: "warm omits provisioning stages",
			headers: map[string]string{
				"session-start-type":        "warm",
				"platform-preprocessing-ms": "91",
				"container-response-ms":     "1662",
				"response-begin-ms":         "1753",
			},
			want: "Platform latency (warm): response headers 1753 ms\n" +
				"  preprocess 91 ms | container 1662 ms\n",
		},
		{
			name: "resume with partial boundaries",
			headers: map[string]string{
				"session-start-type":        "resume",
				"platform-preprocessing-ms": "200",
			},
			want: "Platform latency (resume)\n  preprocess 200 ms\n",
		},
		{
			name: "zero remains a measurement",
			headers: map[string]string{
				"session-start-type":        "warm",
				"platform-preprocessing-ms": "0",
				"container-response-ms":     "0",
				"response-begin-ms":         "0",
			},
			want: "Platform latency (warm): response headers 0 ms\n  preprocess 0 ms | container 0 ms\n",
		},
		{
			name:         "background only reports platform overhead",
			platformOnly: true,
			headers: map[string]string{
				"session-start-type":        "cold",
				"platform-preprocessing-ms": "28",
				"infra-setup-ms":            "900",
				"container-readiness-ms":    "1700",
				"container-response-ms":     "372",
				"response-begin-ms":         "3000",
			},
			want: "Platform latency (cold, async; platform overhead only)\n" +
				"  preprocess 28 ms | infra 900 ms | readiness 1700 ms\n",
		},
		{
			name: "missing headers",
			want: "Platform latency: not returned by the service.\n",
		},
		{
			name: "unknown headers are not exposed",
			headers: map[string]string{
				"internal-value": "must-not-be-shown",
			},
			want: "Platform latency: not returned by the service.\n",
		},
		{
			name: "missing start type",
			headers: map[string]string{
				"platform-preprocessing-ms": "42",
			},
			want: "Platform latency (unknown)\n  preprocess 42 ms\n",
		},
		{
			name: "large milliseconds do not overflow a duration conversion",
			headers: map[string]string{
				"response-begin-ms": "9223372036854775807",
			},
			want: "Platform latency (unknown): response headers 9223372036854775807 ms\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "https://example.com/responses", nil)
			latency := newInvokeLatency(req, true, tt.platformOnly)
			resp := latencyTestResponse(http.StatusOK, tt.headers)
			latency.captureResponse(resp)
			var output bytes.Buffer
			require.NoError(t, latency.writeTo(&output))
			require.Equal(t, tt.want, output.String())
		})
	}
}

func TestInvokeLatencyInvalidFields(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "-1", "1.5", "NaN", "1, 2", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "https://example.com/responses", nil)
			latency := newInvokeLatency(req, true, false)
			latency.captureResponse(latencyTestResponse(http.StatusOK, map[string]string{
				"session-start-type":        "warm",
				"platform-preprocessing-ms": value,
				"container-response-ms":     "5",
			}))
			var output bytes.Buffer
			require.NoError(t, latency.writeTo(&output))
			require.Contains(t, output.String(), "container 5 ms")
			require.Contains(t, output.String(),
				"WARNING: Ignored invalid platform latency fields: platform-preprocessing-ms.")
			require.NotContains(t, output.String(), "preprocess 0 ms")
		})
	}
}

func TestInvokeLatencyRejectsDuplicateAndUntrustedValues(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	latency := newInvokeLatency(req, true, false)
	resp := latencyTestResponse(http.StatusOK, map[string]string{
		"session-start-type": "\x1b[31mspoofed",
		"response-begin-ms":  "10",
	})
	resp.Header.Add(invokeLatencyHeaderPrefix+"response-begin-ms", "20")
	latency.captureResponse(resp)
	var output bytes.Buffer
	require.NoError(t, latency.writeTo(&output))
	require.Contains(t, output.String(), "Platform latency: unavailable.")
	require.Contains(t, output.String(), "session-start-type, response-begin-ms")
	require.NotContains(t, output.String(), "\x1b")
	require.NotContains(t, output.String(), "spoofed")
}

func TestInvokeLatencyCaptureDoesNotReadBody(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	latency := newInvokeLatency(req, true, false)
	body := strings.NewReader("data: live agent output\n\n")
	size := body.Len()
	resp := latencyTestResponse(http.StatusOK, map[string]string{"response-begin-ms": "100"})
	resp.Body = io.NopCloser(body)
	latency.captureResponse(resp)
	resp.Header.Values(invokeLatencyHeaderPrefix + "response-begin-ms")[0] = "200"
	var output bytes.Buffer
	require.NoError(t, latency.writeTo(&output))
	require.Contains(t, output.String(), "response headers 100 ms")
	require.Equal(t, size, body.Len())
}

func TestInvokeLatencyCapturePreservesOriginalMetrics(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	latency := newInvokeLatency(req, true, false)
	latency.captureResponse(latencyTestResponse(http.StatusOK, map[string]string{
		"session-start-type": "warm",
		"response-begin-ms":  "100",
	}))
	latency.captureResponse(latencyTestResponse(http.StatusOK, nil))
	latency.captureResponse(latencyTestResponse(http.StatusInternalServerError, map[string]string{
		"response-begin-ms": "999",
	}))
	var output bytes.Buffer
	require.NoError(t, latency.writeTo(&output))
	require.Equal(t, "Platform latency (warm): response headers 100 ms\n", output.String())
}

func TestInvokeLatencyRawOutput(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	latency := newInvokeLatency(req, true, false)
	resp := latencyTestResponse(http.StatusOK, map[string]string{
		"session-start-type": "warm",
		"response-begin-ms":  "100",
	})
	resp.Body = io.NopCloser(strings.NewReader("data: original body\n\n"))
	latency.captureResponse(resp)
	var output bytes.Buffer
	require.NoError(t, writeRawResponse(&output, resp))
	require.Contains(t, output.String(), "X-Ms-Debug-Latency-Response-Begin-Ms: 100\r\n")
	require.True(t, strings.HasSuffix(output.String(), "\r\n\r\ndata: original body\n\n"))
	require.NotContains(t, output.String(), "Platform latency")
}

func TestInvokeLatencyWriterError(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "https://example.com/responses", nil)
	latency := newInvokeLatency(req, true, false)
	reader, writer := io.Pipe()
	require.NoError(t, reader.CloseWithError(io.ErrClosedPipe))
	t.Cleanup(func() { _ = writer.Close() })
	require.ErrorIs(t, latency.writeTo(writer), io.ErrClosedPipe)
}

func TestInvokeLatencyLROPolling(t *testing.T) {
	previousInterval := defaultLROPollInterval
	defaultLROPollInterval = time.Millisecond
	t.Cleanup(func() { defaultLROPollInterval = previousInterval })

	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected poll method: %s", r.Method)
		}
		if r.Header.Get(invokeLatencyHeaderPrefix+"enabled") != "" {
			t.Error("poll requests must not opt in again")
		}
		w.Header().Set("Content-Type", "application/json")
		if polls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"status":"in_progress"}`)
			return
		}
		w.Header().Set(invokeLatencyHeaderPrefix+"session-start-type", "cold")
		w.Header().Set(invokeLatencyHeaderPrefix+"platform-preprocessing-ms", "25")
		w.Header().Set(invokeLatencyHeaderPrefix+"infra-setup-ms", "100")
		w.Header().Set(invokeLatencyHeaderPrefix+"container-readiness-ms", "200")
		_, _ = io.WriteString(w, `{"status":"completed","result":"latency-demo-ok"}`)
	}))
	defer server.Close()

	req := httptest.NewRequest(http.MethodPost, server.URL+"/invocations?api-version=v1", nil)
	latency := newInvokeLatency(req, true, false)
	resp := latencyTestResponse(http.StatusAccepted, map[string]string{
		"session-start-type":        "cold",
		"platform-preprocessing-ms": "10",
		"container-response-ms":     "999",
		"response-begin-ms":         "1000",
	})
	resp.Request = req
	resp.Header.Set("x-agent-invocation-id", "inv-latency")
	resp.Body = io.NopCloser(strings.NewReader(`{"status":"accepted"}`))
	var invokeErr error
	withCapturedStdout(t, func() {
		invokeErr = handleInvocationResponse(t.Context(), resp, "", "", "test-agent", time.Second, "v1", nil, false, latency)
	})
	require.NoError(t, invokeErr)
	require.EqualValues(t, 2, polls.Load())

	var output bytes.Buffer
	require.NoError(t, latency.writeTo(&output))
	require.Equal(t, "Platform latency (cold, async; platform overhead only)\n"+
		"  preprocess 25 ms | infra 100 ms | readiness 200 ms\n", output.String())
}

func latencyTestResponse(status int, fields map[string]string) *http.Response {
	headers := make(http.Header)
	for suffix, value := range fields {
		headers.Set(invokeLatencyHeaderPrefix+suffix, value)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: http.NoBody}
}
