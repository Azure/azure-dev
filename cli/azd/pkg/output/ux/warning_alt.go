// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/pkg/output"
)

// Warning message with the prefix "(!) Warning: "
type WarningAltMessage struct {
	Message string
}

func (d *WarningAltMessage) ToString(currentIndentation string) string {
	if currentIndentation == "" {
		currentIndentation = "  "
	}
	return fmt.Sprintf("%s%s %s", currentIndentation, warningPrefix, d.Message)
}

func (d *WarningAltMessage) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s %s", warningPrefix, d.Message)))
}
