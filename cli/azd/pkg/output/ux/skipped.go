// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type SkippedMessage struct {
	Message string
}

func (d *SkippedMessage) ToString(currentIndentation string) string {
	if currentIndentation == "" {
		currentIndentation = "  "
	}
	return fmt.Sprintf("%s%s %s", currentIndentation, skippedPrefix, d.Message)
}

func (d *SkippedMessage) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s %s", skippedPrefix, d.Message)))
}
