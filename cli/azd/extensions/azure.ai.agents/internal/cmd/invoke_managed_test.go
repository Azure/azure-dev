// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
)

// TestStreamManagedSSE_TextDeltas asserts only output_text.delta events are
// rendered, in order, with a trailing newline, and that lifecycle events are
// consumed silently.
func TestStreamManagedSSE_TextDeltas(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":", world"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n")

	var out strings.Builder
	if _, err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	got := out.String()
	want := "Hello, world\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestStreamManagedSSE_NoText asserts that a stream with no text deltas
// produces no output (and notably no trailing newline).
func TestStreamManagedSSE_NoText(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed"}`,
		"",
	}, "\n")

	var out strings.Builder
	if _, err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if out.String() != "" {
		t.Errorf("expected empty output, got %q", out.String())
	}
}

// TestStreamManagedSSE_IgnoresMalformedData asserts a malformed data line does
// not abort the stream or emit garbage.
func TestStreamManagedSSE_IgnoresMalformedData(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.output_text.delta",
		`data: {not valid json`,
		"",
		"event: response.output_text.delta",
		`data: {"delta":"ok"}`,
		"",
	}, "\n")

	var out strings.Builder
	if _, err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if out.String() != "ok\n" {
		t.Errorf("got %q, want %q", out.String(), "ok\n")
	}
}

// TestStreamManagedSSE_CapturesResponseID asserts the response id is parsed
// from lifecycle events so the caller can chain the next turn via
// previous_response_id. The last id seen (from response.completed) wins.
func TestStreamManagedSSE_CapturesResponseID(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_created"}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","delta":"hi"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_done"}}`,
		"",
	}, "\n")

	var out strings.Builder
	id, err := streamManagedSSE(strings.NewReader(sse), &out)
	if err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if id != "resp_done" {
		t.Errorf("got response id %q, want %q", id, "resp_done")
	}
	if out.String() != "hi\n" {
		t.Errorf("got %q, want %q", out.String(), "hi\n")
	}
}
