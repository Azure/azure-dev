// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
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

// getExtensionIdFromContext extracts the extension ID from the gRPC metadata context.
func (ops *EventMessageEnvelope) getExtensionIdFromContext(ctx context.Context) string {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return ""
	}
	return claims.Subject
}

// GetRequestId generates a correlation key from the message content and context.
// For EventMessage, the correlation key is generated from extension.Id (from context) + eventName + serviceName.
func (ops *EventMessageEnvelope) GetRequestId(ctx context.Context, msg *EventMessage) string {
	extensionId := ops.getExtensionIdFromContext(ctx)
	if extensionId == "" {
		return ""
	}

	// Generate correlation key based on message type
	innerMsg := ops.GetInnerMessage(msg)
	if innerMsg == nil {
		return ""
	}

	switch v := innerMsg.(type) {
	case *SubscribeProjectEvent:
		// Project event subscriptions: extension.id + first event name
		// Use first event name to match correlation with invoke requests
		if len(v.EventNames) > 0 {
			return fmt.Sprintf("%s.%s", extensionId, v.EventNames[0])
		}
		return ""
	case *ProjectHandlerStatus:
		// Project events: extension.id + event name
		return fmt.Sprintf("%s.%s", extensionId, v.EventName)
	case *InvokeProjectHandler:
		// Server-sent invoke messages use same correlation as status responses
		return fmt.Sprintf("%s.%s", extensionId, v.EventName)
	case *SubscribeServiceEvent:
		// Service event subscriptions: extension.id + first event name
		// Use first event name to match correlation with invoke requests
		if len(v.EventNames) > 0 {
			return fmt.Sprintf("%s.%s", extensionId, v.EventNames[0])
		}
		return ""
	case *ServiceHandlerStatus:
		// Service events: extension.id + service name + event name
		return fmt.Sprintf("%s.%s.%s", extensionId, v.ServiceName, v.EventName)
	case *InvokeServiceHandler:
		// Server-sent invoke messages use same correlation as status responses
		return fmt.Sprintf("%s.%s.%s", extensionId, v.Service.Name, v.EventName)
	}

	return ""
}

// SetRequestId is a no-op for EventMessage as it doesn't have a RequestId field.
// Correlation is managed through message content (event names).
func (ops *EventMessageEnvelope) SetRequestId(ctx context.Context, msg *EventMessage, id string) {
	// No-op: EventMessage doesn't have a RequestId field
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

// GetTraceParent returns the W3C traceparent value from the message for distributed tracing.
func (ops *EventMessageEnvelope) GetTraceParent(msg *EventMessage) string {
	return msg.GetTraceParent()
}

// SetTraceParent sets the W3C traceparent value on the message for distributed tracing.
func (ops *EventMessageEnvelope) SetTraceParent(msg *EventMessage, traceParent string) {
	msg.TraceParent = traceParent
}

// GetInnerMessage returns the inner message from the oneof field
func (ops *EventMessageEnvelope) GetInnerMessage(msg *EventMessage) any {
	// The MessageType field is a oneof wrapper. We need to extract the actual inner message.
	switch m := msg.MessageType.(type) {
	case *EventMessage_SubscribeProjectEvent:
		return m.SubscribeProjectEvent
	case *EventMessage_InvokeProjectHandler:
		return m.InvokeProjectHandler
	case *EventMessage_ProjectHandlerStatus:
		return m.ProjectHandlerStatus
	case *EventMessage_SubscribeServiceEvent:
		return m.SubscribeServiceEvent
	case *EventMessage_InvokeServiceHandler:
		return m.InvokeServiceHandler
	case *EventMessage_ServiceHandlerStatus:
		return m.ServiceHandlerStatus
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
