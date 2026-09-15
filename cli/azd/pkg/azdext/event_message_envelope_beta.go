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

type betaEventMessageEnvelope struct {
	extensionID string
}

var _ grpcbroker.MessageEnvelope[v1beta.EventMessage] = (*betaEventMessageEnvelope)(nil)

func newBetaEventMessageEnvelope(extensionID string) *betaEventMessageEnvelope {
	return &betaEventMessageEnvelope{extensionID: extensionID}
}

func (ops *betaEventMessageEnvelope) extensionIDFromContext(ctx context.Context) string {
	claims, err := extensions.GetClaimsFromContext(ctx)
	if err == nil && claims.Subject != "" {
		return claims.Subject
	}

	return ops.extensionID
}

func (ops *betaEventMessageEnvelope) GetRequestId(ctx context.Context, msg *v1beta.EventMessage) string {
	innerMsg := ops.GetInnerMessage(msg)
	if output, ok := innerMsg.(*v1beta.HandlerOutput); ok {
		return output.RequestId
	}

	extensionID := ops.extensionIDFromContext(ctx)
	if extensionID == "" || innerMsg == nil {
		return ""
	}

	switch value := innerMsg.(type) {
	case *v1beta.SubscribeProjectEvent:
		if len(value.EventNames) > 0 {
			return fmt.Sprintf("%s.%s", extensionID, value.EventNames[0])
		}
	case *v1beta.ProjectHandlerStatus:
		return fmt.Sprintf("%s.%s", extensionID, value.EventName)
	case *v1beta.InvokeProjectHandler:
		return fmt.Sprintf("%s.%s", extensionID, value.EventName)
	case *v1beta.SubscribeServiceEvent:
		if len(value.EventNames) > 0 {
			return fmt.Sprintf("%s.%s", extensionID, value.EventNames[0])
		}
	case *v1beta.ServiceHandlerStatus:
		return fmt.Sprintf("%s.%s.%s", extensionID, value.ServiceName, value.EventName)
	case *v1beta.InvokeServiceHandler:
		return fmt.Sprintf("%s.%s.%s", extensionID, value.Service.Name, value.EventName)
	}

	return ""
}

func (ops *betaEventMessageEnvelope) SetRequestId(context.Context, *v1beta.EventMessage, string) {}

func (ops *betaEventMessageEnvelope) GetError(*v1beta.EventMessage) error {
	return nil
}

func (ops *betaEventMessageEnvelope) SetError(*v1beta.EventMessage, error) {}

func (ops *betaEventMessageEnvelope) GetInnerMessage(msg *v1beta.EventMessage) any {
	switch message := msg.MessageType.(type) {
	case *v1beta.EventMessage_SubscribeProjectEvent:
		return message.SubscribeProjectEvent
	case *v1beta.EventMessage_InvokeProjectHandler:
		return message.InvokeProjectHandler
	case *v1beta.EventMessage_ProjectHandlerStatus:
		return message.ProjectHandlerStatus
	case *v1beta.EventMessage_SubscribeServiceEvent:
		return message.SubscribeServiceEvent
	case *v1beta.EventMessage_InvokeServiceHandler:
		return message.InvokeServiceHandler
	case *v1beta.EventMessage_ServiceHandlerStatus:
		return message.ServiceHandlerStatus
	case *v1beta.EventMessage_HandlerOutput:
		return message.HandlerOutput
	default:
		return nil
	}
}

func (ops *betaEventMessageEnvelope) IsProgressMessage(msg *v1beta.EventMessage) bool {
	return msg.GetHandlerOutput() != nil
}

func (ops *betaEventMessageEnvelope) GetProgressMessage(msg *v1beta.EventMessage) string {
	if output := msg.GetHandlerOutput(); output != nil {
		return output.Output
	}
	return ""
}

func (ops *betaEventMessageEnvelope) CreateProgressMessage(requestID string, message string) *v1beta.EventMessage {
	return &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_HandlerOutput{
			HandlerOutput: &v1beta.HandlerOutput{
				RequestId: requestID,
				Output:    message,
			},
		},
	}
}
