package vsrpc

import (
	"io"
	"sync"
)

// writerMultiplexer is an io.Writer that writes to multiple io.Writers and allows these writers to be added and removed
// dynamically.
type writerMultiplexer struct {
	writers []io.Writer
	mu      sync.Mutex
}

// Write writes the given bytes to all the writers in the multiplexer.
func (m *writerMultiplexer) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, w := range m.writers {
		n, err = w.Write(p)
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// AddWriter adds a writer to the multiplexer.
func (m *writerMultiplexer) AddWriter(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.writers = append(m.writers, w)
}

// RemoveWriter removes a writer from the multiplexer.
func (m *writerMultiplexer) RemoveWriter(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, writer := range m.writers {
		if writer == w {
			m.writers = append(m.writers[:i], m.writers[i+1:]...)
			return
		}
	}
}

// writerFunc is an io.Writer implemented by a function.
type writerFunc func(p []byte) (n int, err error)

// Write implements the io.Writer interface.
func (f writerFunc) Write(p []byte) (n int, err error) {
	return f(p)
}
