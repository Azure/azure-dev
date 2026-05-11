// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// proxyFetchParams mirrors ProxyFetchParams in webViewRpcContracts.ts.
type proxyFetchParams struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type proxyFetchResult struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

type proxyInvokeParams struct {
	RequestID string            `json:"requestId"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
}

// proxyInvokeResult covers both buffered and streaming branches. The
// streaming branch omits Body; the buffered branch includes it.
type proxyInvokeResult struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	Mode       string            `json:"mode"`
	Body       string            `json:"body,omitempty"`
}

// httpClient is shared across requests in a session. The Python
// reference uses a 300s timeout; we mirror that for consistency.
var sessionHTTPClient = &http.Client{
	Timeout: 300 * time.Second,
}

// assertLocalhost is a defense-in-depth check. The inspector should
// only proxy localhost calls; the SPA never has a reason to ask for an
// external URL through the proxy. The VS Code reference implementation
// enforces the same check (see webviewProxyHandler.ts:6-13).
func assertLocalhost(rawURL string, label string) error {
	// Accept the simple cases without parsing for speed; URL parsing
	// is a fallback for unusual hostnames.
	lower := strings.ToLower(rawURL)
	if strings.HasPrefix(lower, "http://localhost") ||
		strings.HasPrefix(lower, "https://localhost") ||
		strings.HasPrefix(lower, "http://127.0.0.1") ||
		strings.HasPrefix(lower, "https://127.0.0.1") ||
		strings.HasPrefix(lower, "ws://localhost") ||
		strings.HasPrefix(lower, "wss://localhost") ||
		strings.HasPrefix(lower, "ws://127.0.0.1") ||
		strings.HasPrefix(lower, "wss://127.0.0.1") {
		return nil
	}
	return fmt.Errorf("%s only allows requests to localhost", label)
}

func (s *rpcSession) proxyFetch(raw json.RawMessage) (any, error) {
	var p proxyFetchParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := assertLocalhost(p.URL, "Proxy fetch"); err != nil {
		return nil, err
	}

	method := p.Method
	if method == "" {
		method = http.MethodGet
	}

	var bodyReader io.Reader
	if p.Body != "" {
		bodyReader = bytes.NewReader([]byte(p.Body))
	}

	req, err := http.NewRequestWithContext(s.rootCtx, method, p.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	resp, err := sessionHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return proxyFetchResult{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    flattenHeaders(resp.Header),
		Body:       string(body),
	}, nil
}

// proxyInvoke matches rpc_handler.py:_proxy_invoke. POST a request,
// then branch on the response Content-Type:
//   - text/event-stream: spawn an SSE pump; return immediately with
//     {mode: "streaming"}.
//   - everything else: read the full body; return {mode: "buffered"}.
func (s *rpcSession) proxyInvoke(raw json.RawMessage) (any, error) {
	var p proxyInvokeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	if err := assertLocalhost(p.URL, "Proxy invoke"); err != nil {
		return nil, err
	}

	isResponses := strings.Contains(p.URL, "/responses")
	if isResponses {
		s.logger.Printf("invoke [%s] POST %s body: %s", p.RequestID, p.URL, p.Body)
	}

	var bodyReader io.Reader
	if p.Body != "" {
		bodyReader = bytes.NewReader([]byte(p.Body))
	}

	// We do not use the session-level cancel registry for the request
	// itself when streaming, because cancellation flows in via the
	// per-stream context registered in pumpSSE.
	req, err := http.NewRequestWithContext(s.rootCtx, http.MethodPost, p.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	resp, err := sessionHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	contentType := resp.Header.Get("Content-Type")
	headers := flattenHeaders(resp.Header)

	if strings.Contains(contentType, "text/event-stream") {
		// Streaming branch: hand the live response to the SSE pump.
		// pumpSSE owns Body.Close().
		go s.pumpSSE(p.RequestID, resp, isResponses)
		return proxyInvokeResult{
			Status:     resp.StatusCode,
			StatusText: resp.Status,
			Headers:    headers,
			Mode:       "streaming",
		}, nil
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if isResponses {
		s.logger.Printf("invoke [%s] response %d: %s", p.RequestID, resp.StatusCode, string(body))
	}
	return proxyInvokeResult{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    headers,
		Mode:       "buffered",
		Body:       string(body),
	}, nil
}

// flattenHeaders collapses http.Header (which permits multiple values)
// into a single string per key. The Python reference does the same via
// dict(resp.headers); the inspector only ever reads scalar headers.
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) == 0 {
			continue
		}
		out[strings.ToLower(k)] = v[0]
	}
	return out
}
