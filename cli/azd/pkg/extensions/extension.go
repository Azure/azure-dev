// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// Extension represents an installed extension.
type Extension struct {
	Id                string           `json:"id"`
	Namespace         string           `json:"namespace"`
	Capabilities      []CapabilityType `json:"capabilities,omitempty"`
	DisplayName       string           `json:"displayName"`
	Description       string           `json:"description"`
	Version           string           `json:"version"`
	Usage             string           `json:"usage"`
	Path              string           `json:"path"`
	Source            string           `json:"source"`
	Providers         []Provider       `json:"providers,omitempty"`
	McpConfig         *McpConfig       `json:"mcp,omitempty"`
	LastUpdateWarning string           `json:"lastUpdateWarning,omitempty"`

	stdin  *bytes.Buffer
	stdout *output.DynamicMultiWriter
	stderr *output.DynamicMultiWriter

	readySignal chan error // consolidated channel, buffered with capacity 1
	readyOnce   sync.Once  // ensures signal is sent only once
	initialized bool
}

// init initializes the extension's buffers and signals.
func (e *Extension) ensureInit() {
	if e.initialized {
		return
	}

	e.stdin = &bytes.Buffer{}
	e.stdout = output.NewDynamicMultiWriter()
	e.stderr = output.NewDynamicMultiWriter()
	e.readySignal = make(chan error, 1)

	e.initialized = true
}

// Initialize signals that the extension is ready.
func (e *Extension) Initialize() {
	e.ensureInit()

	e.readyOnce.Do(func() {
		e.readySignal <- nil
	})
}

// Fail signals that the extension has encountered an error.
func (e *Extension) Fail(err error) {
	e.ensureInit()

	e.readyOnce.Do(func() {
		e.readySignal <- err
	})
}

// WaitUntilReady blocks until the extension signals readiness or failure.
func (e *Extension) WaitUntilReady(ctx context.Context) error {
	e.ensureInit()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-e.readySignal:
		return err
	}
}

// HasCapability checks if the extension has the specified capabilities.
func (e *Extension) HasCapability(capability ...CapabilityType) bool {
	for _, cap := range capability {
		found := false
		for _, existing := range e.Capabilities {
			if existing == cap {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// StdIn returns the standard input buffer for the extension.
func (e *Extension) StdIn() io.Reader {
	e.ensureInit()
	return e.stdin
}

// StdOut returns the standard output writer for the extension.
func (e *Extension) StdOut() *output.DynamicMultiWriter {
	e.ensureInit()
	return e.stdout
}

// StdErr returns the standard error writer for the extension.
func (e *Extension) StdErr() *output.DynamicMultiWriter {
	e.ensureInit()
	return e.stderr
}
