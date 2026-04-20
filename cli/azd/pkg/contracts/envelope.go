// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

import "time"

type EventDataType string

const (
	ConsoleMessageEventDataType EventDataType = "consoleMessage"
)

type EventEnvelope struct {
	Type      EventDataType `json:"type"`
	Timestamp time.Time     `json:"timestamp"`
	Data      any           `json:"data"`
}

// ErrorEnvelope is the standard envelope for error returns.
type ErrorEnvelope[D any] struct {
	// Code is a machine-readable error code.
	Code string `json:"code"`
	// Message is a human-readable error message.
	Message string `json:"message"`
	// Details contains additional error details.
	Details D `json:"details"`
}
