// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// JsonEventWriter writes compact JSON events to an append-only file.
type JsonEventWriter struct {
	path string

	mu      sync.Mutex
	file    *os.File
	encoder *json.Encoder
	refs    int
	err     error
	buffers map[string][]byte
}

// NewJsonEventWriter creates a JSON event writer for path.
// An empty path disables the writer.
func NewJsonEventWriter(path string) *JsonEventWriter {
	return &JsonEventWriter{path: path}
}

// Enabled reports whether an output path was configured.
func (w *JsonEventWriter) Enabled() bool {
	return w != nil && w.path != ""
}

// Open opens the configured file for append.
func (w *JsonEventWriter) Open() error {
	if !w.Enabled() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		w.refs++
		return nil
	}

	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening JSON output file %q: %w", w.path, err)
	}

	w.file = file
	w.encoder = json.NewEncoder(file)
	w.refs = 1
	w.err = nil
	w.buffers = map[string][]byte{}
	return nil
}

// Write writes one compact JSON value followed by a newline.
func (w *JsonEventWriter) Write(event any) error {
	if !w.Enabled() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.err != nil {
		return w.err
	}
	if w.encoder == nil {
		w.err = fmt.Errorf("JSON output file %q is not open", w.path)
		return w.err
	}

	return w.encodeLocked(event)
}

func (w *JsonEventWriter) encodeLocked(event any) error {
	if err := w.encoder.Encode(event); err != nil {
		w.err = fmt.Errorf("writing JSON output file %q: %w", w.path, err)
	}
	return w.err
}

func (w *JsonEventWriter) writeStream(stream string, p []byte) error {
	if !w.Enabled() || len(p) == 0 {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.err != nil {
		return w.err
	}
	if w.encoder == nil {
		w.err = fmt.Errorf("JSON output file %q is not open", w.path)
		return w.err
	}

	w.buffers[stream] = append(w.buffers[stream], p...)
	for {
		newline := bytes.IndexByte(w.buffers[stream], '\n')
		if newline < 0 {
			break
		}

		line := w.buffers[stream][:newline]
		w.buffers[stream] = w.buffers[stream][newline+1:]
		if err := w.writeStreamLineLocked(stream, line); err != nil {
			return err
		}
	}

	return nil
}

func (w *JsonEventWriter) writeStreamLineLocked(stream string, line []byte) error {
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if carriageReturn := bytes.LastIndexByte(line, '\r'); carriageReturn >= 0 {
		line = line[carriageReturn+1:]
	}
	if len(line) == 0 {
		return nil
	}

	return w.encodeLocked(EventForStreamMessage(string(line), stream))
}

// FlushStreams writes any unterminated stdout or stderr content as console events.
func (w *JsonEventWriter) FlushStreams() error {
	if !w.Enabled() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.err != nil {
		return w.err
	}
	for stream, line := range w.buffers {
		if len(line) > 0 {
			if err := w.writeStreamLineLocked(stream, line); err != nil {
				return err
			}
		}
		delete(w.buffers, stream)
	}

	return nil
}

// Err returns the first write error encountered by the writer.
func (w *JsonEventWriter) Err() error {
	if w == nil {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// Close closes the output file after all users have released it.
func (w *JsonEventWriter) Close() error {
	if !w.Enabled() {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return w.err
	}

	w.refs--
	if w.refs > 0 {
		return w.err
	}

	closeErr := w.file.Close()
	w.file = nil
	w.encoder = nil
	w.refs = 0
	w.buffers = nil

	if closeErr != nil && w.err == nil {
		w.err = fmt.Errorf("closing JSON output file %q: %w", w.path, closeErr)
	}

	return w.err
}

type jsonEventStreamWriter struct {
	writer io.Writer
	events *JsonEventWriter
	stream string
}

// NewJsonEventStreamWriter mirrors complete lines written to writer into the JSON event stream.
func NewJsonEventStreamWriter(writer io.Writer, events *JsonEventWriter, stream string) io.Writer {
	if events == nil || !events.Enabled() {
		return writer
	}

	return &jsonEventStreamWriter{
		writer: writer,
		events: events,
		stream: stream,
	}
}

func (w *jsonEventStreamWriter) Write(p []byte) (int, error) {
	n, writeErr := w.writer.Write(p)
	var eventErr error
	if n > 0 {
		eventErr = w.events.writeStream(w.stream, p[:n])
	}
	return n, errors.Join(writeErr, eventErr)
}
