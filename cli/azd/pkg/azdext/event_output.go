// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type eventOutputContextKey struct{}

type eventOutputWriter struct {
	writer   io.Writer
	progress grpcbroker.ProgressFunc
	mu       sync.Mutex
}

const maxProgressMessageBytes = 32 * 1024

var eventOutputFallback io.Writer = os.Stdout

// EventOutput returns the writer for output from a lifecycle handler.
//
// Output written during an active lifecycle invocation is sent to the
// host with the invocation's request ID and also written to stdout.
// Calls made outside a lifecycle invocation write to standard output.
func EventOutput(ctx context.Context) io.Writer {
	if ctx != nil {
		if writer, ok := ctx.Value(eventOutputContextKey{}).(io.Writer); ok {
			return writer
		}
	}

	return eventOutputFallback
}

func withEventOutput(
	ctx context.Context,
	writer io.Writer,
	progress grpcbroker.ProgressFunc,
) context.Context {
	if writer == nil {
		writer = eventOutputFallback
	}

	return context.WithValue(ctx, eventOutputContextKey{}, &eventOutputWriter{
		writer:   writer,
		progress: progress,
	})
}

func (w *eventOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	output := data
	if w.progress != nil {
		if len(data) == 0 {
			w.progress("")
		} else {
			for len(data) > 0 {
				chunkSize := min(len(data), maxProgressMessageBytes)
				w.progress(string(data[:chunkSize]))
				data = data[chunkSize:]
			}
		}
	}

	return w.writer.Write(output)
}
