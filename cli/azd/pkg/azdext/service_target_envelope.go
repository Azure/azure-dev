// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
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

// GetError returns the error from the message as a Go error type.
// It returns an ExtensionResponseError that preserves structured error information for telemetry.
func (ops *ServiceTargetEnvelope) GetError(msg *ServiceTargetMessage) error {
	return UnwrapErrorFromServiceTarget(msg.Error)
}

// SetError sets an error on the message.
// It extracts structured error information from known error types like azcore.ResponseError.
func (ops *ServiceTargetEnvelope) SetError(msg *ServiceTargetMessage, err error) {
	msg.Error = WrapErrorForServiceTarget(err)
}

// GetInnerMessage returns the inner message from the oneof field
func (ops *ServiceTargetEnvelope) GetInnerMessage(msg *ServiceTargetMessage) any {
	// The MessageType field is a oneof wrapper. We need to extract the actual inner message.
	switch m := msg.MessageType.(type) {
	case *ServiceTargetMessage_RegisterServiceTargetRequest:
		return m.RegisterServiceTargetRequest
	case *ServiceTargetMessage_RegisterServiceTargetResponse:
		return m.RegisterServiceTargetResponse
	case *ServiceTargetMessage_InitializeRequest:
		return m.InitializeRequest
	case *ServiceTargetMessage_InitializeResponse:
		return m.InitializeResponse
	case *ServiceTargetMessage_GetTargetResourceRequest:
		return m.GetTargetResourceRequest
	case *ServiceTargetMessage_GetTargetResourceResponse:
		return m.GetTargetResourceResponse
	case *ServiceTargetMessage_DeployRequest:
		return m.DeployRequest
	case *ServiceTargetMessage_DeployResponse:
		return m.DeployResponse
	case *ServiceTargetMessage_ProgressMessage:
		return m.ProgressMessage
	case *ServiceTargetMessage_PackageRequest:
		return m.PackageRequest
	case *ServiceTargetMessage_PackageResponse:
		return m.PackageResponse
	case *ServiceTargetMessage_PublishRequest:
		return m.PublishRequest
	case *ServiceTargetMessage_PublishResponse:
		return m.PublishResponse
	case *ServiceTargetMessage_EndpointsRequest:
		return m.EndpointsRequest
	case *ServiceTargetMessage_EndpointsResponse:
		return m.EndpointsResponse
	default:
		// Return nil for unhandled message types
		return nil
	}
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

// GetTraceParent returns the W3C traceparent header value from the message.
// This is used to propagate distributed tracing context across the gRPC boundary.
func (ops *ServiceTargetEnvelope) GetTraceParent(msg *ServiceTargetMessage) string {
	return msg.TraceParent
}

// SetTraceParent sets the W3C traceparent header value on the message.
// This is used to propagate distributed tracing context across the gRPC boundary.
func (ops *ServiceTargetEnvelope) SetTraceParent(msg *ServiceTargetMessage, traceParent string) {
	msg.TraceParent = traceParent
}
