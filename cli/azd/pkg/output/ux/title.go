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

type MessageTitle struct {
	Title     string `json:"Title"`
	TitleNote string `json:"Note"`
}

func (t *MessageTitle) ToString(currentIndentation string) string {
	if t.TitleNote != "" {
		return fmt.Sprintf("\n%s\n%s\n",
			output.WithBold(t.Title),
			output.WithGrayFormat(t.TitleNote))
	}
	return fmt.Sprintf("\n%s\n", output.WithBold(t.Title))
}

func (t *MessageTitle) MarshalJSON() ([]byte, error) {
	// reusing the same envelope from console messages
	return json.Marshal(
		contracts.EventEnvelope{
			Type:      contracts.OperationStart,
			Timestamp: time.Now(),
			Data:      t,
		},
	)
}
