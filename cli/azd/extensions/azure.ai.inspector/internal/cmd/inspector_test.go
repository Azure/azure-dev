// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
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

func TestReadSSEStreamDrainsAfterCompleted(t *testing.T) {
	pr, pw := io.Pipe()
	writeDone := make(chan error, 1)
	readDone := make(chan error, 1)

	go func() {
		readDone <- readSSEStream(pr, "local")
	}()

	go func() {
		_, err := fmt.Fprint(
			pw,
			"event: response.output_text.delta\n",
			"data: {\"delta\":\"hello\"}\n",
			"\n",
			"event: response.completed\n",
			"data: {\"response\":{\"status\":\"completed\"}}\n",
			"\n",
			"event: response.output_text.delta\n",
			"data: {\"delta\":\"trailing\"}\n",
			"\n",
		)
		if err == nil {
			err = pw.Close()
		}
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write stream: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writer blocked after response.completed")
	}

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("readSSEStream returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readSSEStream did not finish after source closed")
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
