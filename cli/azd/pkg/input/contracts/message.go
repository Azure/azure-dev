// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package contracts contains API contracts that azd CLI communicates externally in commands via stdout.
// Currently, all contracts support JSON output.

package contracts

const ConsoleMessageType string = "consoleMessage"

type ConsoleMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func NewConsoleMessage(msg string) ConsoleMessage {
	return ConsoleMessage{
		Type:    ConsoleMessageType,
		Message: msg,
	}
}
