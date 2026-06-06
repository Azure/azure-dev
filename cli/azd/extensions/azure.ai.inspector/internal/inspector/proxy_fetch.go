// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	maxProxyBufferedBodyBytes = 16 << 20
	maxProxyErrorBodyBytes    = 4 << 10
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

// sessionHTTPClient is for one-shot fetch/invoke calls. The 5-minute timeout
// is generous for buffered responses but would silently truncate long-lived
// streams, so streamHTTPClient below has no timeout for SSE pumps.
var sessionHTTPClient = &http.Client{
	Timeout: 300 * time.Second,
}

// streamHTTPClient is used for SSE pumps (proxyFetchSSE / pumpSSE). No
// Client.Timeout — lifecycle is bound to the per-stream context, so the
// stream lasts as long as the agent keeps emitting events.
var streamHTTPClient = &http.Client{}

func (s *rpcSession) proxyFetch(raw json.RawMessage) (any, error) {
	var p proxyFetchParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}
	targetURL, err := validateAgentProxyURL(p.URL, s.cfg.AgentPort)
	if err != nil {
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

	req, err := http.NewRequestWithContext(s.rootCtx, method, targetURL.String(), bodyReader)
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

	body, err := readLimitedBody(resp.Body, maxProxyBufferedBodyBytes)
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
	targetURL, err := validateAgentProxyURL(p.URL, s.cfg.AgentPort)
	if err != nil {
		return nil, err
	}

	s.logger.Printf("invoke [%s] POST %s body length: %d", p.RequestID, targetURL.Redacted(), len(p.Body))

	var bodyReader io.Reader
	if p.Body != "" {
		bodyReader = bytes.NewReader([]byte(p.Body))
	}

	req, err := http.NewRequestWithContext(s.rootCtx, http.MethodPost, targetURL.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}

	// Use streamHTTPClient: when the response turns out to be SSE we hand
	// resp.Body off to pumpSSE which can run for many minutes, longer than
	// sessionHTTPClient's 5-minute timeout would allow.
	resp, err := streamHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	contentType := resp.Header.Get("Content-Type")
	headers := flattenHeaders(resp.Header)

	if strings.Contains(contentType, "text/event-stream") {
		go s.pumpSSE(p.RequestID, resp, true)
		return proxyInvokeResult{
			Status:     resp.StatusCode,
			StatusText: resp.Status,
			Headers:    headers,
			Mode:       "streaming",
		}, nil
	}

	defer resp.Body.Close()
	body, err := readLimitedBody(resp.Body, maxProxyBufferedBodyBytes)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	s.logger.Printf("invoke [%s] response %d body length: %d", p.RequestID, resp.StatusCode, len(body))
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

func readLimitedBody(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBytes)
	}
	return body, nil
}

func readTruncatedBody(r io.Reader, maxBytes int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) <= maxBytes {
		return body, false, nil
	}
	return body[:maxBytes], true, nil
}

// printUserInput echoes the user's text from a Responses API request body.
// The SPA sends `input` as message items: [{content:[{type:"input_text", text:"..."}]}].
func printUserInput(body string) {
	var p struct {
		Input []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if json.Unmarshal([]byte(body), &p) != nil {
		return
	}
	for _, item := range p.Input {
		for _, c := range item.Content {
			if c.Type == "input_text" && c.Text != "" {
				fmt.Fprintf(os.Stderr, "[user] %s\n", c.Text)
			}
		}
	}
}
