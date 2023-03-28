// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type WarningMessage struct {
	Description string
	HidePrefix  bool
}

func (t *WarningMessage) ToString(currentIndentation string) string {
	var prefix string
	if !t.HidePrefix {
		prefix = "Warning: "
	}
	return output.WithWarningFormat("%s%s%s", currentIndentation, prefix, t.Description)
}

func (t *WarningMessage) MarshalJSON() ([]byte, error) {
	var prefix string
	if !t.HidePrefix {
		prefix = "Warning: "
	}

	return json.Marshal(output.EventForMessage(fmt.Sprintf("%s%s", prefix, t.Description)))
}
