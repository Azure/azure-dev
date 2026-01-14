// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// Warning message with hidable prefix "WARNING: "
type WarningMessage struct {
	Description string
	HidePrefix  bool
	// Hints are optional additional lines displayed as bullets below the warning
	Hints []string
}

func (t *WarningMessage) ToString(currentIndentation string) string {
	var prefix string
	if !t.HidePrefix {
		prefix = "WARNING: "
	}

	var sb strings.Builder
	sb.WriteString(output.WithWarningFormat("%s%s%s", currentIndentation, prefix, t.Description))

	// Render hints as bulleted lines (not in warning color)
	for _, hint := range t.Hints {
		sb.WriteString(fmt.Sprintf("\n%s  \u2022 %s", currentIndentation, hint))
	}

	return sb.String()
}

func (t *WarningMessage) MarshalJSON() ([]byte, error) {
	var prefix string
	if !t.HidePrefix {
		prefix = "WARNING: "
	}

	msg := fmt.Sprintf("%s%s", prefix, t.Description)
	for _, hint := range t.Hints {
		msg += fmt.Sprintf("\n  \u2022 %s", hint)
	}

	return json.Marshal(output.EventForMessage(msg))
}
