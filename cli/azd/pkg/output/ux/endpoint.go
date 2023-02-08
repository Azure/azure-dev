// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type Endpoint struct {
	Endpoint string
}

func (e *Endpoint) ToString(currentIndentation string) string {
	return fmt.Sprintf("%s- Endpoint: %s", currentIndentation, output.WithLinkFormat(e.Endpoint))
}

func (e *Endpoint) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(
		contracts.EventEnvelope{
			Type:      contracts.EndpointEventDataType,
			Timestamp: time.Now(),
			Data: contracts.EndpointEventData{
				Endpoint: e.Endpoint,
			},
		},
	)
}
