// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// messageWriter is an io.Writer that writes to an IObserver[ProgressMessage], emitting a message for each write.
type messageWriter struct {
	ctx             context.Context
	observer        *Observer[ProgressMessage]
	messageTemplate ProgressMessage
}

// Write implements io.Writer.
func (mw *messageWriter) Write(p []byte) (int, error) {
	err := mw.observer.OnNext(mw.ctx, mw.messageTemplate.WithMessage(string(p)))
	if err != nil {
		return 0, err
	}

	return len(p), nil
}

// lineWriter is an io.Writer that writes to another io.Writer, emitting a message for each line written.
type lineWriter struct {
	// The next writer to write to.
	next io.Writer
	// If true, trim line endings from the written lines.
	trimLineEndings bool

	buf bytes.Buffer
	// bufMu protects access to buf.
	bufMu sync.Mutex
}

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.bufMu.Lock()
	defer lw.bufMu.Unlock()

	for i, b := range p {
		lw.buf.WriteByte(b)

		if b == '\n' {
			if lw.trimLineEndings {
				dropped := 1
				if lw.buf.Len() > 1 && lw.buf.Bytes()[lw.buf.Len()-2] == '\r' {
					dropped = 2
				}
				lw.buf.Truncate(lw.buf.Len() - dropped)
			}

			_, err := lw.next.Write(lw.buf.Bytes())
			if err != nil {
				return i + 1, err
			}

			lw.buf.Reset()
		}
	}

	return len(p), nil
}

func (lw *lineWriter) Flush(ctx context.Context) error {
	lw.bufMu.Lock()
	defer lw.bufMu.Unlock()

	if lw.buf.Len() > 0 {
		_, err := lw.next.Write(lw.buf.Bytes())
		lw.buf.Reset()
		return err
	}

	return nil
}
