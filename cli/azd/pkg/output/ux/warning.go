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
	Description string `json:"Description"`
}

func (t *WarningMessage) ToString(currentIndentation string) string {
	return output.WithWarningFormat("Warning: %s", t.Description)
}

func (t *WarningMessage) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(
		contracts.EventEnvelope{
			Type:      contracts.Warning,
			Timestamp: time.Now(),
			Data:      t,
		},
	)
}
