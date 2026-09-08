// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"bytes"
	"context"
	"io"
	"slices"
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
	initOnce    sync.Once  // ensures buffers and signals are initialized only once
	readyOnce   sync.Once  // ensures signal is sent only once

	reportedError error      // structured error reported by the extension via gRPC
	errorMu       sync.Mutex // guards reportedError
}

// init initializes the extension's buffers and signals.
func (e *Extension) ensureInit() {
	e.initOnce.Do(func() {
		e.stdin = &bytes.Buffer{}
		e.stdout = output.NewDynamicMultiWriter()
		e.stderr = output.NewDynamicMultiWriter()
		e.readySignal = make(chan error, 1)
	})
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
		found := slices.Contains(e.Capabilities, cap)
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

// SetReportedError stores a structured error reported by the extension via gRPC.
func (e *Extension) SetReportedError(err error) {
	e.errorMu.Lock()
	defer e.errorMu.Unlock()
	e.reportedError = err
}

// GetReportedError returns the structured error reported by the extension, if any.
func (e *Extension) GetReportedError() error {
	e.errorMu.Lock()
	defer e.errorMu.Unlock()
	return e.reportedError
}
