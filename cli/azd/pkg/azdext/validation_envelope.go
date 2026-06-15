// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// ValidationEnvelope provides message operations for ValidationMessage.
// It implements the grpcbroker.MessageEnvelope interface.
type ValidationEnvelope struct{}

// NewValidationEnvelope creates a new ValidationEnvelope instance.
func NewValidationEnvelope() *ValidationEnvelope {
	return new(ValidationEnvelope)
}

// Verify interface implementation at compile time
var _ grpcbroker.MessageEnvelope[ValidationMessage] = (*ValidationEnvelope)(nil)

// GetRequestId returns the request ID from the message.
func (ops *ValidationEnvelope) GetRequestId(
	_ context.Context, msg *ValidationMessage,
) string {
	return msg.RequestId
}

// SetRequestId sets the request ID on the message.
func (ops *ValidationEnvelope) SetRequestId(
	_ context.Context, msg *ValidationMessage, id string,
) {
	msg.RequestId = id
}

// GetError returns the error from the message as a Go error type.
func (ops *ValidationEnvelope) GetError(msg *ValidationMessage) error {
	return UnwrapError(msg.Error)
}

// SetError sets an error on the message.
func (ops *ValidationEnvelope) SetError(
	msg *ValidationMessage, err error,
) {
	msg.Error = WrapError(err)
}

// GetInnerMessage returns the inner message from the oneof field.
func (ops *ValidationEnvelope) GetInnerMessage(
	msg *ValidationMessage,
) any {
	switch m := msg.MessageType.(type) {
	case *ValidationMessage_RegisterValidationCheckRequest:
		return m.RegisterValidationCheckRequest
	case *ValidationMessage_RegisterValidationCheckResponse:
		return m.RegisterValidationCheckResponse
	case *ValidationMessage_ValidationCheckRequest:
		return m.ValidationCheckRequest
	case *ValidationMessage_ValidationCheckResponse:
		return m.ValidationCheckResponse
	case *ValidationMessage_PrepareValidationContextChunk:
		return m.PrepareValidationContextChunk
	case *ValidationMessage_PrepareValidationContextResponse:
		return m.PrepareValidationContextResponse
	default:
		return nil
	}
}

// IsProgressMessage returns true if the message is a progress message.
// Validation messages do not support progress; always returns false.
func (ops *ValidationEnvelope) IsProgressMessage(
	_ *ValidationMessage,
) bool {
	return false
}

// GetProgressMessage returns empty string — validation has no progress.
func (ops *ValidationEnvelope) GetProgressMessage(
	_ *ValidationMessage,
) string {
	return ""
}

// CreateProgressMessage creates a progress message envelope.
// Validation does not use progress messages, but the interface
// requires this method. Returns a minimal message.
func (ops *ValidationEnvelope) CreateProgressMessage(
	requestId string, _ string,
) *ValidationMessage {
	return &ValidationMessage{RequestId: requestId}
}
