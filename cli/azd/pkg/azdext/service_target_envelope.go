// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/cli/azd/internal/grpcbroker"
)

// ServiceTargetEnvelope provides message operations for ServiceTargetMessage
// It implements the grpcbroker.MessageOperations interface
type ServiceTargetEnvelope struct{}

// NewServiceTargetEnvelope creates a new ServiceTargetMessageOps instance
func NewServiceTargetEnvelope() *ServiceTargetEnvelope {
	return &ServiceTargetEnvelope{}
}

// Verify interface implementation at compile time
var _ grpcbroker.MessageEnvelope[ServiceTargetMessage] = (*ServiceTargetEnvelope)(nil)

// GetRequestId returns the request ID from the message
func (ops *ServiceTargetEnvelope) GetRequestId(ctx context.Context, msg *ServiceTargetMessage) string {
	return msg.RequestId
}

// SetRequestId sets the request ID on the message
func (ops *ServiceTargetEnvelope) SetRequestId(ctx context.Context, msg *ServiceTargetMessage, id string) {
	msg.RequestId = id
}

// GetError returns the error from the message as a Go error type
func (ops *ServiceTargetEnvelope) GetError(msg *ServiceTargetMessage) error {
	if msg.Error == nil || msg.Error.Message == "" {
		return nil
	}
	return errors.New(msg.Error.Message)
}

// SetError sets an error on the message
func (ops *ServiceTargetEnvelope) SetError(msg *ServiceTargetMessage, err error) {
	if err == nil {
		msg.Error = nil
		return
	}
	msg.Error = &ServiceTargetErrorMessage{
		Message: err.Error(),
	}
}

// GetInnerMessage returns the inner message from the oneof field
func (ops *ServiceTargetEnvelope) GetInnerMessage(msg *ServiceTargetMessage) any {
	return msg.MessageType
}

// IsProgressMessage returns true if the message contains a progress message
func (ops *ServiceTargetEnvelope) IsProgressMessage(msg *ServiceTargetMessage) bool {
	return msg.GetProgressMessage() != nil
}

// GetProgressMessage extracts the progress message text from a progress message.
// Returns empty string if the message is not a progress message.
func (ops *ServiceTargetEnvelope) GetProgressMessage(msg *ServiceTargetMessage) string {
	if progressMsg := msg.GetProgressMessage(); progressMsg != nil {
		return progressMsg.GetMessage()
	}
	return ""
}

// CreateProgressMessage creates a new progress message envelope with the given text.
// This is used by server-side handlers to send progress updates back to clients.
func (ops *ServiceTargetEnvelope) CreateProgressMessage(requestId string, message string) *ServiceTargetMessage {
	return &ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &ServiceTargetMessage_ProgressMessage{
			ProgressMessage: &ServiceTargetProgressMessage{
				RequestId: requestId,
				Message:   message,
			},
		},
	}
}
