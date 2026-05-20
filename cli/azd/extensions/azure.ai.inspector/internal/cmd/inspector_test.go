// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestInjectSSEEventsSynthesizesEventLines(t *testing.T) {
	input := "data: {\"type\":\"response.output_text.delta\"}\n\n"

	output, err := io.ReadAll(injectSSEEvents(strings.NewReader(input)))
	if err != nil {
		t.Fatalf("read injected stream: %v", err)
	}

	got := string(output)
	if !strings.Contains(got, "event: response.output_text.delta\n") {
		t.Fatalf("output should include synthesized event line, got %q", got)
	}
	if !strings.Contains(got, input) {
		t.Fatalf("output should preserve original data line, got %q", got)
	}
}

func TestInjectSSEEventsPropagatesReadError(t *testing.T) {
	wantErr := errors.New("read failed")

	_, err := io.ReadAll(injectSSEEvents(errorReader{err: wantErr}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("read error = %v, want %v", err, wantErr)
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
