// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type betaEventMessageEnvelope struct{}

var _ grpcbroker.MessageEnvelope[v1beta.EventMessage] = (*betaEventMessageEnvelope)(nil)

func newBetaEventMessageEnvelope() *betaEventMessageEnvelope {
	return &betaEventMessageEnvelope{}
}

// NewBetaEventMessageEnvelope creates a beta event message envelope.
func NewBetaEventMessageEnvelope() grpcbroker.MessageEnvelope[v1beta.EventMessage] {
	return newBetaEventMessageEnvelope()
}

func (e *betaEventMessageEnvelope) GetRequestId(
	ctx context.Context, msg *v1beta.EventMessage,
) string {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil {
		return ""
	}
	id := claims.Subject
	if id == "" {
		return ""
	}
	switch m := e.GetInnerMessage(msg).(type) {
	case *v1beta.SubscribeProjectEvent:
		if len(m.EventNames) > 0 {
			return fmt.Sprintf("%s.%s", id, m.EventNames[0])
		}
	case *v1beta.ProjectHandlerStatus:
		return fmt.Sprintf("%s.%s", id, m.EventName)
	case *v1beta.InvokeProjectHandler:
		return fmt.Sprintf("%s.%s", id, m.EventName)
	case *v1beta.SubscribeServiceEvent:
		if len(m.EventNames) > 0 {
			return fmt.Sprintf("%s.%s", id, m.EventNames[0])
		}
	case *v1beta.ServiceHandlerStatus:
		return fmt.Sprintf("%s.%s.%s", id, m.ServiceName, m.EventName)
	case *v1beta.InvokeServiceHandler:
		if m.Service != nil {
			return fmt.Sprintf("%s.%s.%s", id, m.Service.Name, m.EventName)
		}
	}
	return ""
}

func (*betaEventMessageEnvelope) SetRequestId(
	context.Context, *v1beta.EventMessage, string,
) {
}

func (*betaEventMessageEnvelope) GetError(*v1beta.EventMessage) error {
	return nil
}

func (*betaEventMessageEnvelope) SetError(
	*v1beta.EventMessage, error,
) {
}

func (*betaEventMessageEnvelope) IsProgressMessage(*v1beta.EventMessage) bool {
	return false
}

func (*betaEventMessageEnvelope) GetProgressMessage(*v1beta.EventMessage) string {
	return ""
}

func (*betaEventMessageEnvelope) CreateProgressMessage(
	string, string,
) *v1beta.EventMessage {
	return nil
}

func (*betaEventMessageEnvelope) GetInnerMessage(
	msg *v1beta.EventMessage,
) any {
	if msg == nil {
		return nil
	}
	switch m := msg.MessageType.(type) {
	case *v1beta.EventMessage_SubscribeProjectEvent:
		return m.SubscribeProjectEvent
	case *v1beta.EventMessage_InvokeProjectHandler:
		return m.InvokeProjectHandler
	case *v1beta.EventMessage_ProjectHandlerStatus:
		return m.ProjectHandlerStatus
	case *v1beta.EventMessage_SubscribeServiceEvent:
		return m.SubscribeServiceEvent
	case *v1beta.EventMessage_InvokeServiceHandler:
		return m.InvokeServiceHandler
	case *v1beta.EventMessage_ServiceHandlerStatus:
		return m.ServiceHandlerStatus
	default:
		return nil
	}
}
