// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/cli/azd/internal/grpcbroker"
)

// FrameworkServiceEnvelope provides message operations for FrameworkServiceMessage
// It implements the grpcbroker.MessageEnvelope interface
type FrameworkServiceEnvelope struct{}

// NewFrameworkServiceEnvelope creates a new FrameworkServiceEnvelope instance
func NewFrameworkServiceEnvelope() *FrameworkServiceEnvelope {
	return &FrameworkServiceEnvelope{}
}

// Verify interface implementation at compile time
var _ grpcbroker.MessageEnvelope[FrameworkServiceMessage] = (*FrameworkServiceEnvelope)(nil)

// GetRequestId returns the request ID from the message
func (ops *FrameworkServiceEnvelope) GetRequestId(ctx context.Context, msg *FrameworkServiceMessage) string {
	return msg.RequestId
}

// SetRequestId sets the request ID on the message
func (ops *FrameworkServiceEnvelope) SetRequestId(ctx context.Context, msg *FrameworkServiceMessage, id string) {
	msg.RequestId = id
}

// GetError returns the error from the message as a Go error type
func (ops *FrameworkServiceEnvelope) GetError(msg *FrameworkServiceMessage) error {
	if msg.Error == nil || msg.Error.Message == "" {
		return nil
	}
	return errors.New(msg.Error.Message)
}

// SetError sets an error on the message
func (ops *FrameworkServiceEnvelope) SetError(msg *FrameworkServiceMessage, err error) {
	if err == nil {
		msg.Error = nil
		return
	}
	msg.Error = &FrameworkServiceErrorMessage{
		Message: err.Error(),
	}
}

// GetInnerMessage returns the inner message from the oneof field
func (ops *FrameworkServiceEnvelope) GetInnerMessage(msg *FrameworkServiceMessage) any {
	return msg.MessageType
}

// IsProgressMessage returns true if the message contains a progress message
func (ops *FrameworkServiceEnvelope) IsProgressMessage(msg *FrameworkServiceMessage) bool {
	return msg.GetProgressMessage() != nil
}

// GetProgressMessage extracts the progress message text from a progress message.
// Returns empty string if the message is not a progress message.
func (ops *FrameworkServiceEnvelope) GetProgressMessage(msg *FrameworkServiceMessage) string {
	if progressMsg := msg.GetProgressMessage(); progressMsg != nil {
		return progressMsg.GetMessage()
	}
	return ""
}

// CreateProgressMessage creates a new progress message envelope with the given text.
// This is used by server-side handlers to send progress updates back to clients.
func (ops *FrameworkServiceEnvelope) CreateProgressMessage(requestId string, message string) *FrameworkServiceMessage {
	return &FrameworkServiceMessage{
		RequestId: requestId,
		MessageType: &FrameworkServiceMessage_ProgressMessage{
			ProgressMessage: &FrameworkServiceProgressMessage{
				RequestId: requestId,
				Message:   message,
			},
		},
	}
}
