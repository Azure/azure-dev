// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package contracts contains API contracts that azd CLI communicates externally in commands via stdout.
// Currently, all contracts support JSON output.
package contracts

import (
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
)

type ConsoleMessage struct {
	Message string `json:"message"`
}

func NewConsoleMessage(msg string) contracts.EventEnvelope {
	return contracts.EventEnvelope{
		Type:      contracts.ConsoleMessageEventDataType,
		Timestamp: time.Now(),
		Data: ConsoleMessage{
			Message: msg,
		},
	}
}
