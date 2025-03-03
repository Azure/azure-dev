// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"io"
	"sync"
)

// DynamicMultiWriter allows adding/removing writers dynamically
type DynamicMultiWriter struct {
	mu      sync.Mutex
	writers []io.Writer
}

func NewDynamicMultiWriter(writers ...io.Writer) *DynamicMultiWriter {
	if len(writers) == 0 {
		writers = []io.Writer{io.Discard}
	}

	return &DynamicMultiWriter{
		writers: writers,
	}
}

// Write writes data to all registered writers
func (d *DynamicMultiWriter) Write(p []byte) (n int, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, w := range d.writers {
		if _, err := w.Write(p); err != nil {
			return 0, err
		}
	}

	return len(p), nil
}

// AddWriter adds a new writer
func (d *DynamicMultiWriter) AddWriter(w io.Writer) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.writers = append(d.writers, w)
}

// RemoveWriter removes a writer
func (d *DynamicMultiWriter) RemoveWriter(target io.Writer) {
	d.mu.Lock()
	defer d.mu.Unlock()

	newWriters := []io.Writer{}
	for _, w := range d.writers {
		if w != target {
			newWriters = append(newWriters, w)
		}
	}

	d.writers = newWriters
}
