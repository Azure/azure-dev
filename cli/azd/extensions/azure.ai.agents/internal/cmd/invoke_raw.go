// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
)

// writeRawResponse writes the response status line, alphabetically-sorted
// canonical headers, a blank line, and the body bytes verbatim to w. Output
// mirrors `curl -i`. Headers are sorted to give the user a deterministic view
// across invocations. Header values from a single key are emitted on
// consecutive lines (one value per line) to preserve any duplicates the server
// sent.
//
// The body is streamed through io.Copy so SSE (text/event-stream) responses
// pass through unbuffered: each chunk reaches the user's terminal as the
// server emits it. The caller remains responsible for closing resp.Body.
//
// writeRawResponse returns an error only when the underlying writer or body
// stream errors mid-flight. It does not parse the body or interpret the status
// code, so 4xx/5xx responses are dumped exactly like 2xx ones; the caller is
// expected to surface a separate, structured error after the dump completes.
func writeRawResponse(w io.Writer, resp *http.Response) error {
	if resp == nil {
		return fmt.Errorf("writeRawResponse: nil response")
	}

	proto := resp.Proto
	if proto == "" {
		proto = "HTTP/1.1"
	}
	status := resp.Status
	if status == "" {
		status = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	if _, err := fmt.Fprintf(w, "%s %s\r\n", proto, status); err != nil {
		return err
	}

	for _, key := range slices.Sorted(maps.Keys(resp.Header)) {
		canonical := http.CanonicalHeaderKey(key)
		for _, value := range resp.Header.Values(key) {
			if _, err := fmt.Fprintf(w, "%s: %s\r\n", canonical, value); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprint(w, "\r\n"); err != nil {
		return err
	}

	if resp.Body == nil {
		return nil
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("failed to write raw response body: %w", err)
	}
	return nil
}
