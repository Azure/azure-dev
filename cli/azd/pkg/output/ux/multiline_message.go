// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type MultilineMessage struct {
	Lines []string
}

func (mm *MultilineMessage) ToString(currentIndentation string) string {
	updatedLines := make([]string, len(mm.Lines))
	for i, line := range mm.Lines {
		if len(line) > 0 {
			updatedLines[i] = currentIndentation + line
		}
	}
	return strings.Join(updatedLines, "\n")
}

func (mm *MultilineMessage) Envelope() contracts.EventEnvelope {
	// reusing the same envelope from console messages
	return output.EventForMessage(strings.Join(mm.Lines, ","))
}
