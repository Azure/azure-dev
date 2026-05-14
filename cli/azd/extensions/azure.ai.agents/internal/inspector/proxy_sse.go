// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type proxyFetchSSEParams struct {
	RequestID string            `json:"requestId"`
	URL       string            `json:"url"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
}

// proxyFetchSSE opens a streaming HTTP request and emits one
// fetchSSE/chunk notification per `data: ` line, then fetchSSE/done.
func (s *rpcSession) proxyFetchSSE(raw json.RawMessage) {
	var p proxyFetchSSEParams
	if err := json.Unmarshal(raw, &p); err != nil {
		s.logger.Printf("fetchSSE: bad params: %v", err)
		return
	}

	printUserInput(p.Body)

	method := p.Method
	if method == "" {
		method = http.MethodGet
	}

	streamCtx, cancel := context.WithCancel(s.rootCtx)
	s.registerStream(p.RequestID, cancel)

	go func() {
		defer s.unregisterStream(p.RequestID)

		var bodyReader io.Reader
		if p.Body != "" {
			bodyReader = bytes.NewReader([]byte(p.Body))
		}
		req, err := http.NewRequestWithContext(streamCtx, method, p.URL, bodyReader)
		if err != nil {
			s.sendSSEDone(p.RequestID, err)
			return
		}
		for k, v := range p.Headers {
			req.Header.Set(k, v)
		}

		resp, err := streamHTTPClient.Do(req)
		if err != nil {
			if streamCtx.Err() != nil {
				return
			}
			s.sendSSEDone(p.RequestID, err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			if requestID := resp.Header.Get("apim-request-id"); requestID != "" {
				fmt.Printf("Trace ID: %s\n", requestID)
			}
			body, _ := io.ReadAll(resp.Body)
			err := fmt.Errorf("POST %s failed with HTTP %d: %s\n%s", p.URL, resp.StatusCode, resp.Status, string(body))
			fmt.Fprintln(os.Stderr, "Error:", err)
			s.sendSSEDone(p.RequestID, err)
			return
		}

		s.streamSSELines(streamCtx, p.RequestID, resp.Body, true)
	}()
}

// pumpSSE streams an already-open response body as SSE chunk/done
// notifications. Used by proxyInvoke when the response is event-stream.
//
// The body has no request context attached (proxyInvoke completed the HTTP
// round-trip synchronously), so a hung Read on Body cannot be cancelled by
// ctx alone. The goroutine below force-closes Body when streamCtx is
// cancelled — that's what unblocks Read on session shutdown. We mirror that
// by cancelling streamCtx ourselves on the normal-completion path so the
// goroutine always runs and closes Body exactly once.
func (s *rpcSession) pumpSSE(requestID string, resp *http.Response, logRaw bool) {
	streamCtx, cancel := context.WithCancel(s.rootCtx)
	s.registerStream(requestID, cancel)
	defer s.unregisterStream(requestID)

	go func() {
		<-streamCtx.Done()
		_ = resp.Body.Close()
	}()

	s.streamSSELines(streamCtx, requestID, resp.Body, logRaw)
	cancel()
}

// streamSSELines emits one chunk notification per `data: ` line. When a
// SSESink is configured the raw body is also teed to it.
func (s *rpcSession) streamSSELines(ctx context.Context, requestID string, body io.Reader, logRaw bool) {
	source := body
	var sinkWriter *io.PipeWriter
	if s.cfg.SSESink != nil {
		pr, pw := io.Pipe()
		sinkWriter = pw
		source = io.TeeReader(body, pw)
		go s.cfg.SSESink(pr)
	}
	defer func() {
		if sinkWriter != nil {
			_ = sinkWriter.Close()
		}
	}()

	reader := bufio.NewReader(source)

	for {
		if ctx.Err() != nil {
			return
		}

		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data: ") {
				payload := line[len("data: "):]
				s.sendNotification("webviewProxy/fetchSSE/chunk", map[string]any{
					"requestId": requestID,
					"data":      payload,
				})
				if logRaw {
					s.logger.Printf("sse chunk [%s]: %s", requestID, payload)
				}
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				s.sendSSEDone(requestID, nil)
				return
			}
			if ctx.Err() != nil {
				return
			}
			s.sendSSEDone(requestID, err)
			return
		}
	}
}

func (s *rpcSession) sendSSEDone(requestID string, err error) {
	payload := map[string]any{"requestId": requestID}
	if err != nil {
		payload["error"] = fmt.Sprintf("%v", err)
	}
	s.sendNotification("webviewProxy/fetchSSE/done", payload)
}
