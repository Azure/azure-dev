// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"io"
	"sync"
)

// messageWriter is an io.Writer that writes to an IObserver[ProgressMessage], emitting a message for each write.
type messageWriter struct {
	ctx             context.Context
	observer        IObserver[ProgressMessage]
	messageTemplate ProgressMessage
}

// lineWriter is an io.Writer that writes to another io.Writer, emitting a message for each line written.
type lineWriter struct {
	next io.Writer
	buf  []byte
	// bufMu protects access to buf.
	bufMu sync.Mutex
}

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.bufMu.Lock()
	defer lw.bufMu.Unlock()

	for i, b := range p {
		lw.buf = append(lw.buf, b)

		if b == '\n' {
			_, err := lw.next.Write(lw.buf)
			if err != nil {
				return i + 1, err
			}

			lw.buf = nil
		}
	}

	return len(p), nil
}

// Flush sends any remaining output to the observer.
func (mw *lineWriter) Flush(ctx context.Context) error {
	mw.bufMu.Lock()
	defer mw.bufMu.Unlock()

	if len(mw.buf) > 0 {
		buf := mw.buf
		mw.buf = nil

		_, err := mw.next.Write(buf)
		return err
	}

	return nil
}

// Write implements io.Writer.
func (mw *messageWriter) Write(p []byte) (int, error) {
	err := mw.observer.OnNext(mw.ctx, mw.messageTemplate.Fill(string(p)))
	if err != nil {
		return 0, err
	}

	return len(p), nil
}
