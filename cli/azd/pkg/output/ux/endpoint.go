// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ux

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type Endpoint struct {
	Endpoint string
}

func (e *Endpoint) ToString(currentIndentation string) string {
	return fmt.Sprintf("%s- Endpoint: %s", currentIndentation, output.WithLinkFormat(e.Endpoint))
}

func (e *Endpoint) MarshalJSON() ([]byte, error) {
	return json.Marshal(output.EventForMessage(fmt.Sprintf("- Endpoint: %s", e.Endpoint)))
}
