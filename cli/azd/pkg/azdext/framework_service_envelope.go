// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/pkg/grpcbroker"
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
	// The MessageType field is a oneof wrapper. We need to extract the actual inner message.
	switch m := msg.MessageType.(type) {
	case *FrameworkServiceMessage_RegisterFrameworkServiceRequest:
		return m.RegisterFrameworkServiceRequest
	case *FrameworkServiceMessage_RegisterFrameworkServiceResponse:
		return m.RegisterFrameworkServiceResponse
	case *FrameworkServiceMessage_InitializeRequest:
		return m.InitializeRequest
	case *FrameworkServiceMessage_InitializeResponse:
		return m.InitializeResponse
	case *FrameworkServiceMessage_RequiredExternalToolsRequest:
		return m.RequiredExternalToolsRequest
	case *FrameworkServiceMessage_RequiredExternalToolsResponse:
		return m.RequiredExternalToolsResponse
	case *FrameworkServiceMessage_RequirementsRequest:
		return m.RequirementsRequest
	case *FrameworkServiceMessage_RequirementsResponse:
		return m.RequirementsResponse
	case *FrameworkServiceMessage_RestoreRequest:
		return m.RestoreRequest
	case *FrameworkServiceMessage_RestoreResponse:
		return m.RestoreResponse
	case *FrameworkServiceMessage_BuildRequest:
		return m.BuildRequest
	case *FrameworkServiceMessage_BuildResponse:
		return m.BuildResponse
	case *FrameworkServiceMessage_PackageRequest:
		return m.PackageRequest
	case *FrameworkServiceMessage_PackageResponse:
		return m.PackageResponse
	case *FrameworkServiceMessage_ProgressMessage:
		return m.ProgressMessage
	default:
		// Return nil for unhandled message types
		return nil
	}
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
