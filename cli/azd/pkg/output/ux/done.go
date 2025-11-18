// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type DoneMessage struct {
	Message string
}

func (d *DoneMessage) ToString(currentIndentation string) string {
	if currentIndentation == "" {
		currentIndentation = "  "
	}
	return fmt.Sprintf("%s%s %s", currentIndentation, donePrefix, d.Message)
}

func (d *DoneMessage) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(output.EventForMessage(
		fmt.Sprintf("%s %s", donePrefix, d.Message)))
}
