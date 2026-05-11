// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inspector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type proxyFetchSSEParams struct {
	RequestID string            `json:"requestId"`
	URL       string            `json:"url"`
	Method    string            `json:"method,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
}

// proxyFetchSSE matches rpc_handler.py:_proxy_sse. Opens a streaming
// HTTP request, splits the body on newlines, emits one
// webviewProxy/fetchSSE/chunk notification per `data: ` line, then a
// final webviewProxy/fetchSSE/done notification.
//
// Returns nil immediately; the actual streaming runs in a goroutine.
func (s *rpcSession) proxyFetchSSE(raw json.RawMessage) {
	var p proxyFetchSSEParams
	if err := json.Unmarshal(raw, &p); err != nil {
		s.logger.Printf("fetchSSE: bad params: %v", err)
		return
	}
	if err := assertLocalhost(p.URL, "Proxy SSE fetch"); err != nil {
		s.logger.Printf("fetchSSE: %v", err)
		return
	}

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

		resp, err := sessionHTTPClient.Do(req)
		if err != nil {
			if streamCtx.Err() != nil {
				// Cancelled by client; the cancel side already accounted
				// for cleanup. No done notification needed.
				return
			}
			s.sendSSEDone(p.RequestID, err)
			return
		}
		defer resp.Body.Close()

		s.streamSSELines(streamCtx, p.RequestID, resp.Body, false)
		// streamSSELines emits the done notification on success or
		// surfaceable errors; cancellation paths suppress it.
	}()
}

// pumpSSE is the streaming branch of proxyInvoke. The HTTP response
// is already open; we only need to register a cancel context, stream
// the body, and emit the same chunk/done notifications. When
// logRaw is true, every raw `data:` payload is also written to the
// session logger (gated by --debug).
func (s *rpcSession) pumpSSE(requestID string, resp *http.Response, logRaw bool) {
	streamCtx, cancel := context.WithCancel(s.rootCtx)
	s.registerStream(requestID, cancel)

	defer s.unregisterStream(requestID)
	defer resp.Body.Close()

	// Cancel the stream if the parent context dies, even if the body
	// is mid-read; closing the body here ensures the scanner returns.
	go func() {
		<-streamCtx.Done()
		// Best-effort: closing while ReadAll is in progress unblocks it.
		_ = resp.Body.Close()
	}()

	s.streamSSELines(streamCtx, requestID, resp.Body, logRaw)
}

// streamSSELines is the shared SSE consumer. It buffers bytes, splits
// on `\n`, and emits notifications for `data: ` lines. The contract
// matches rpc_handler.py exactly (lines 138-155, 195-216). When
// logRaw is true, every `data:` payload is also written to s.logger
// (which is gated by --debug at the extension entry point).
func (s *rpcSession) streamSSELines(ctx context.Context, requestID string, body io.Reader, logRaw bool) {
	reader := bufio.NewReader(body)
	var buf bytes.Buffer

	for {
		if ctx.Err() != nil {
			// Cancelled — suppress the done notification, matching the
			// Python reference where cancel.is_set() shortcuts the loop.
			return
		}

		chunk := make([]byte, 4096)
		n, err := reader.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])

			// Drain complete lines from buf.
			for {
				data := buf.Bytes()
				idx := bytes.IndexByte(data, '\n')
				if idx < 0 {
					break
				}
				line := string(data[:idx])
				buf.Next(idx + 1)
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
		}

		if err != nil {
			if err == io.EOF {
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
