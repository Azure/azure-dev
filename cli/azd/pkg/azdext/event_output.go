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

	return os.Stdout
}

func withEventOutput(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) context.Context {
	return context.WithValue(ctx, eventOutputContextKey{}, &eventOutputWriter{
		writer:   os.Stdout,
		progress: progress,
	})
}

func (w *eventOutputWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.progress != nil {
		w.progress(string(data))
	}

	return w.writer.Write(data)
}
