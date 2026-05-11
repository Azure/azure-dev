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

// Body is empty when Mode == "streaming". The SPA branches on Mode.
type proxyInvokeResult struct {
	Status     int               `json:"status"`
	StatusText string            `json:"statusText"`
	Headers    map[string]string `json:"headers"`
	Mode       string            `json:"mode"`
	Body       string            `json:"body,omitempty"`
}

var sessionHTTPClient = &http.Client{
	Timeout: 300 * time.Second,
}

func (s *rpcSession) proxyFetch(raw json.RawMessage) (any, error) {
	var p proxyFetchParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
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

// proxyInvoke POSTs a request and either streams an SSE response via
// pumpSSE (Mode="streaming") or buffers the body (Mode="buffered").
func (s *rpcSession) proxyInvoke(raw json.RawMessage) (any, error) {
	var p proxyInvokeParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	isResponses := strings.Contains(p.URL, "/responses")
	if isResponses {
		s.logger.Printf("invoke [%s] POST %s body: %s", p.RequestID, p.URL, p.Body)
	}

	var bodyReader io.Reader
	if p.Body != "" {
		bodyReader = bytes.NewReader([]byte(p.Body))
	}

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
