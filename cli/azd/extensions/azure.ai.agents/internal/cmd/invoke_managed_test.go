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
	if err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
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
	if err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
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
	if err := streamManagedSSE(strings.NewReader(sse), &out); err != nil {
		t.Fatalf("streamManagedSSE: %v", err)
	}
	if out.String() != "ok\n" {
		t.Errorf("got %q, want %q", out.String(), "ok\n")
	}
}
