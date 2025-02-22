// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"bytes"
	"io"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// Extension represents an installed extension.
type Extension struct {
	Id           string           `json:"id"`
	Namespace    string           `json:"namespace"`
	Capabilities []CapabilityType `json:"capabilities,omitempty"`
	DisplayName  string           `json:"displayName"`
	Description  string           `json:"description"`
	Version      string           `json:"version"`
	Usage        string           `json:"usage"`
	Path         string           `json:"path"`
	Source       string           `json:"source"`

	stdin  *bytes.Buffer
	stdout *output.DynamicMultiWriter
	stderr *output.DynamicMultiWriter
}

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

func (e *Extension) StdIn() io.Reader {
	if e.stdin == nil {
		e.stdin = &bytes.Buffer{}
	}

	return e.stdin
}

func (e *Extension) StdOut() *output.DynamicMultiWriter {
	if e.stdout == nil {
		e.stdout = output.NewDynamicMultiWriter()
	}

	return e.stdout
}

func (e *Extension) StdErr() *output.DynamicMultiWriter {
	if e.stderr == nil {
		e.stderr = output.NewDynamicMultiWriter()
	}

	return e.stderr
}
