// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// EventMessageEnvelope provides message operations for EventMessage
// It implements the grpcbroker.MessageEnvelope interface
// This envelope extracts extension ID from gRPC context for correlation.
type EventMessageEnvelope struct{}

// NewEventMessageEnvelope creates a new EventMessageEnvelope instance.
func NewEventMessageEnvelope() *EventMessageEnvelope {
	return &EventMessageEnvelope{}
}

// Verify interface implementation at compile time
var _ grpcbroker.MessageEnvelope[EventMessage] = (*EventMessageEnvelope)(nil)

// GetRequestId generates a correlation key from the message content and context.
// For EventMessage, the correlation key is generated from extension.Id (from context) + eventName + serviceName.
func (ops *EventMessageEnvelope) GetRequestId(ctx context.Context, msg *EventMessage) string {
	return msg.RequestId
}

// SetRequestId is a no-op for EventMessage as it doesn't have a RequestId field.
// Correlation is managed through message content (event names).
func (ops *EventMessageEnvelope) SetRequestId(ctx context.Context, msg *EventMessage, id string) {
	msg.RequestId = id
}

// GetError returns nil as EventMessage doesn't have an Error field.
// Error handling is done through status strings in handler status messages.
func (ops *EventMessageEnvelope) GetError(msg *EventMessage) error {
	return nil
}

// SetError is a no-op for EventMessage as it doesn't have an Error field.
func (ops *EventMessageEnvelope) SetError(msg *EventMessage, err error) {
	// No-op: EventMessage uses status strings, not Error field
}

// GetInnerMessage returns the inner message from the oneof field
func (ops *EventMessageEnvelope) GetInnerMessage(msg *EventMessage) any {
	// The MessageType field is a oneof wrapper. We need to extract the actual inner message.
	switch m := msg.MessageType.(type) {
	case *EventMessage_SubscribeProjectEventRequest:
		return m.SubscribeProjectEventRequest
	case *EventMessage_SubscribeProjectEventResponse:
		return m.SubscribeProjectEventResponse
	case *EventMessage_InvokeProjectHandlerRequest:
		return m.InvokeProjectHandlerRequest
	case *EventMessage_InvokeProjectHandlerResponse:
		return m.InvokeProjectHandlerResponse
	case *EventMessage_SubscribeServiceEventRequest:
		return m.SubscribeServiceEventRequest
	case *EventMessage_SubscribeServiceEventResponse:
		return m.SubscribeServiceEventResponse
	case *EventMessage_InvokeServiceHandlerRequest:
		return m.InvokeServiceHandlerRequest
	case *EventMessage_InvokeServiceHandlerResponse:
		return m.InvokeServiceHandlerResponse
	default:
		// Return nil for unhandled message types
		return nil
	}
}

// IsProgressMessage returns false as EventMessage doesn't support progress messages
func (ops *EventMessageEnvelope) IsProgressMessage(msg *EventMessage) bool {
	return false
}

// GetProgressMessage returns empty string as EventMessage doesn't support progress messages
func (ops *EventMessageEnvelope) GetProgressMessage(msg *EventMessage) string {
	return ""
}

// CreateProgressMessage returns nil as EventMessage doesn't support progress messages
func (ops *EventMessageEnvelope) CreateProgressMessage(requestId string, message string) *EventMessage {
	return nil
}
