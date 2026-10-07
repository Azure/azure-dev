// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// EventMessageEnvelope provides message operations for EventMessage
// It implements the grpcbroker.MessageEnvelope interface
// This envelope extracts extension ID from gRPC context for correlation.
type EventMessageEnvelope struct {
	extensionId string
}

// NewEventMessageEnvelope creates a new EventMessageEnvelope instance.
func NewEventMessageEnvelope(extensionId ...string) *EventMessageEnvelope {
	envelope := &EventMessageEnvelope{}
	if len(extensionId) > 0 {
		envelope.extensionId = extensionId[0]
	}
	return envelope
}

// Verify interface implementation at compile time
var _ grpcbroker.MessageEnvelope[EventMessage] = (*EventMessageEnvelope)(nil)
var _ grpcbroker.CancellationMessageEnvelope[EventMessage] = (*EventMessageEnvelope)(nil)
var _ grpcbroker.PersistentHandlerContextEnvelope[EventMessage] = (*EventMessageEnvelope)(nil)

const eventHandlerCancelingStatus = "canceling"

// getExtensionIdFromContext extracts the extension ID from the gRPC metadata context.
func (ops *EventMessageEnvelope) getExtensionIdFromContext(ctx context.Context) string {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err == nil && claims.Subject != "" {
		return claims.Subject
	}
	return ops.extensionId
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
		if v.Service == nil {
			return ""
		}
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

// GetInnerMessage returns the inner message from the oneof field
func (ops *EventMessageEnvelope) GetInnerMessage(msg *EventMessage) any {
	if msg == nil {
		return nil
	}

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

// PreserveHandlerContext keeps lifecycle subscriptions registered for the
// lifetime of the event stream. Invocation handlers remain request-scoped so
// cancellation can target only the matching in-flight handler.
func (ops *EventMessageEnvelope) PreserveHandlerContext(_ context.Context, msg *EventMessage) bool {
	switch ops.GetInnerMessage(msg).(type) {
	case *SubscribeProjectEvent, *SubscribeServiceEvent:
		return true
	default:
		return false
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

// CreateCancellationMessage creates a handler-status control message that older
// extension SDKs safely ignore and newer brokers use to cancel the matching handler.
func (ops *EventMessageEnvelope) CreateCancellationMessage(
	_ context.Context,
	request *EventMessage,
	err error,
) *EventMessage {
	if err == nil {
		return nil
	}

	switch invoke := ops.GetInnerMessage(request).(type) {
	case *InvokeProjectHandler:
		return &EventMessage{
			MessageType: &EventMessage_ProjectHandlerStatus{
				ProjectHandlerStatus: &ProjectHandlerStatus{
					EventName: invoke.EventName,
					Status:    eventHandlerCancelingStatus,
					Message:   err.Error(),
					Error:     WrapError(err),
				},
			},
		}
	case *InvokeServiceHandler:
		serviceName := ""
		if invoke.Service != nil {
			serviceName = invoke.Service.Name
		}
		return &EventMessage{
			MessageType: &EventMessage_ServiceHandlerStatus{
				ServiceHandlerStatus: &ServiceHandlerStatus{
					EventName:   invoke.EventName,
					ServiceName: serviceName,
					Status:      eventHandlerCancelingStatus,
					Message:     err.Error(),
					Error:       WrapError(err),
				},
			},
		}
	default:
		return nil
	}
}

// IsCancellationMessage identifies handler-status control messages.
func (ops *EventMessageEnvelope) IsCancellationMessage(_ context.Context, msg *EventMessage) bool {
	err := ops.GetCancellationError(msg)
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	switch statusMsg := ops.GetInnerMessage(msg).(type) {
	case *ProjectHandlerStatus:
		return statusMsg.Status == eventHandlerCancelingStatus
	case *ServiceHandlerStatus:
		return statusMsg.Status == eventHandlerCancelingStatus
	default:
		return false
	}
}

// GetCancellationError returns the cancellation cause carried by a handler-status control message.
func (ops *EventMessageEnvelope) GetCancellationError(msg *EventMessage) error {
	switch statusMsg := ops.GetInnerMessage(msg).(type) {
	case *ProjectHandlerStatus:
		return UnwrapError(statusMsg.Error)
	case *ServiceHandlerStatus:
		return UnwrapError(statusMsg.Error)
	default:
		return nil
	}
}
