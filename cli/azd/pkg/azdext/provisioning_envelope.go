// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// ProvisioningEnvelope provides message operations for ProvisioningMessage.
// It implements the grpcbroker.MessageEnvelope interface.
type ProvisioningEnvelope struct{}

// NewProvisioningEnvelope creates a new ProvisioningEnvelope instance.
func NewProvisioningEnvelope() *ProvisioningEnvelope {
	return &ProvisioningEnvelope{}
}

// Verify interface implementation at compile time
var _ grpcbroker.MessageEnvelope[ProvisioningMessage] = (*ProvisioningEnvelope)(nil)

// GetRequestId returns the request ID from the message.
func (ops *ProvisioningEnvelope) GetRequestId(ctx context.Context, msg *ProvisioningMessage) string {
	return msg.RequestId
}

// SetRequestId sets the request ID on the message.
func (ops *ProvisioningEnvelope) SetRequestId(ctx context.Context, msg *ProvisioningMessage, id string) {
	msg.RequestId = id
}

// GetError returns the error from the message as a Go error type.
// It returns a typed error based on the ErrorOrigin that preserves structured information for telemetry.
func (ops *ProvisioningEnvelope) GetError(msg *ProvisioningMessage) error {
	return UnwrapError(msg.Error)
}

// SetError sets an error on the message.
// It detects the error type and populates the appropriate source details.
func (ops *ProvisioningEnvelope) SetError(msg *ProvisioningMessage, err error) {
	msg.Error = WrapError(err)
}

// GetInnerMessage returns the inner message from the oneof field.
func (ops *ProvisioningEnvelope) GetInnerMessage(msg *ProvisioningMessage) any {
	switch m := msg.MessageType.(type) {
	case *ProvisioningMessage_RegisterProvisioningProviderRequest:
		return m.RegisterProvisioningProviderRequest
	case *ProvisioningMessage_RegisterProvisioningProviderResponse:
		return m.RegisterProvisioningProviderResponse
	case *ProvisioningMessage_InitializeRequest:
		return m.InitializeRequest
	case *ProvisioningMessage_InitializeResponse:
		return m.InitializeResponse
	case *ProvisioningMessage_StateRequest:
		return m.StateRequest
	case *ProvisioningMessage_StateResponse:
		return m.StateResponse
	case *ProvisioningMessage_DeployRequest:
		return m.DeployRequest
	case *ProvisioningMessage_DeployResponse:
		return m.DeployResponse
	case *ProvisioningMessage_PreviewRequest:
		return m.PreviewRequest
	case *ProvisioningMessage_PreviewResponse:
		return m.PreviewResponse
	case *ProvisioningMessage_DestroyRequest:
		return m.DestroyRequest
	case *ProvisioningMessage_DestroyResponse:
		return m.DestroyResponse
	case *ProvisioningMessage_EnsureEnvRequest:
		return m.EnsureEnvRequest
	case *ProvisioningMessage_EnsureEnvResponse:
		return m.EnsureEnvResponse
	case *ProvisioningMessage_ParametersRequest:
		return m.ParametersRequest
	case *ProvisioningMessage_ParametersResponse:
		return m.ParametersResponse
	case *ProvisioningMessage_PlannedOutputsRequest:
		return m.PlannedOutputsRequest
	case *ProvisioningMessage_PlannedOutputsResponse:
		return m.PlannedOutputsResponse
	case *ProvisioningMessage_ProgressMessage:
		return m.ProgressMessage
	default:
		return nil
	}
}

// IsProgressMessage returns true if the message contains a progress message.
func (ops *ProvisioningEnvelope) IsProgressMessage(msg *ProvisioningMessage) bool {
	return msg.GetProgressMessage() != nil
}

// GetProgressMessage extracts the progress message text from a progress message.
// Returns empty string if the message is not a progress message.
func (ops *ProvisioningEnvelope) GetProgressMessage(msg *ProvisioningMessage) string {
	if progressMsg := msg.GetProgressMessage(); progressMsg != nil {
		return progressMsg.GetMessage()
	}
	return ""
}

// CreateProgressMessage creates a new progress message envelope with the given text.
// This is used by server-side handlers to send progress updates back to clients.
func (ops *ProvisioningEnvelope) CreateProgressMessage(
	requestId string,
	message string,
) *ProvisioningMessage {
	return &ProvisioningMessage{
		RequestId: requestId,
		MessageType: &ProvisioningMessage_ProgressMessage{
			ProgressMessage: &ProvisioningProgressMessage{
				RequestId: requestId,
				Message:   message,
				Timestamp: time.Now().UnixMilli(),
			},
		},
	}
}
