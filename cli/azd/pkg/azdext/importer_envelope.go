// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// ImporterEnvelope provides message operations for ImporterMessage
// It implements the grpcbroker.MessageEnvelope interface
type ImporterEnvelope struct{}

// NewImporterEnvelope creates a new ImporterEnvelope instance
func NewImporterEnvelope() *ImporterEnvelope {
	return &ImporterEnvelope{}
}

// Verify interface implementation at compile time
var _ grpcbroker.MessageEnvelope[ImporterMessage] = (*ImporterEnvelope)(nil)

// GetRequestId returns the request ID from the message
func (ops *ImporterEnvelope) GetRequestId(ctx context.Context, msg *ImporterMessage) string {
	return msg.RequestId
}

// SetRequestId sets the request ID on the message
func (ops *ImporterEnvelope) SetRequestId(ctx context.Context, msg *ImporterMessage, id string) {
	msg.RequestId = id
}

// GetError returns the error from the message as a Go error type.
// It returns a typed error based on the ErrorOrigin that preserves structured information for telemetry.
func (ops *ImporterEnvelope) GetError(msg *ImporterMessage) error {
	return UnwrapError(msg.Error)
}

// SetError sets an error on the message.
// It detects the error type and populates the appropriate source details.
func (ops *ImporterEnvelope) SetError(msg *ImporterMessage, err error) {
	msg.Error = WrapError(err)
}

// GetInnerMessage returns the inner message from the oneof field
func (ops *ImporterEnvelope) GetInnerMessage(msg *ImporterMessage) any {
	// The MessageType field is a oneof wrapper. We need to extract the actual inner message.
	switch m := msg.MessageType.(type) {
	case *ImporterMessage_RegisterImporterRequest:
		return m.RegisterImporterRequest
	case *ImporterMessage_RegisterImporterResponse:
		return m.RegisterImporterResponse
	case *ImporterMessage_CanImportRequest:
		return m.CanImportRequest
	case *ImporterMessage_CanImportResponse:
		return m.CanImportResponse
	case *ImporterMessage_ServicesRequest:
		return m.ServicesRequest
	case *ImporterMessage_ServicesResponse:
		return m.ServicesResponse
	case *ImporterMessage_ProjectInfrastructureRequest:
		return m.ProjectInfrastructureRequest
	case *ImporterMessage_ProjectInfrastructureResponse:
		return m.ProjectInfrastructureResponse
	case *ImporterMessage_GenerateAllInfrastructureRequest:
		return m.GenerateAllInfrastructureRequest
	case *ImporterMessage_GenerateAllInfrastructureResponse:
		return m.GenerateAllInfrastructureResponse
	case *ImporterMessage_ProgressMessage:
		return m.ProgressMessage
	default:
		// Return nil for unhandled message types
		return nil
	}
}

// IsProgressMessage returns true if the message contains a progress message
func (ops *ImporterEnvelope) IsProgressMessage(msg *ImporterMessage) bool {
	return msg.GetProgressMessage() != nil
}

// GetProgressMessage extracts the progress message text from a progress message.
// Returns empty string if the message is not a progress message.
func (ops *ImporterEnvelope) GetProgressMessage(msg *ImporterMessage) string {
	if progressMsg := msg.GetProgressMessage(); progressMsg != nil {
		return progressMsg.GetMessage()
	}
	return ""
}

// CreateProgressMessage creates a new progress message envelope with the given text.
// This is used by server-side handlers to send progress updates back to clients.
func (ops *ImporterEnvelope) CreateProgressMessage(requestId string, message string) *ImporterMessage {
	return &ImporterMessage{
		RequestId: requestId,
		MessageType: &ImporterMessage_ProgressMessage{
			ProgressMessage: &ImporterProgressMessage{
				RequestId: requestId,
				Message:   message,
			},
		},
	}
}
