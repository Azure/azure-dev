// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
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
	// reusing the same envelope from console messages
	return json.Marshal(
		contracts.EventEnvelope{
			Type:      contracts.WarningEventDataType,
			Timestamp: time.Now(),
			Data: contracts.WarningEventData{
				Description: t.Description,
				HidePrefix:  t.HidePrefix,
			},
		},
	)
}
