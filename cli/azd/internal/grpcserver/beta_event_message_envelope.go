// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorchain"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type betaEventMessageEnvelope struct{}

var _ grpcbroker.MessageEnvelope[v1beta.EventMessage] = (*betaEventMessageEnvelope)(nil)

func newBetaEventMessageEnvelope() *betaEventMessageEnvelope {
	return &betaEventMessageEnvelope{}
}

func (e *betaEventMessageEnvelope) GetRequestId(
	ctx context.Context,
	msg *v1beta.EventMessage,
) string {
	if msg != nil && msg.RequestId != "" {
		return msg.RequestId
	}

	claims, err := extensions.GetClaimsFromContext(ctx)
	if err != nil || claims.Subject == "" {
		return ""
	}
	extensionID := claims.Subject

	switch inner := e.GetInnerMessage(msg).(type) {
	case *v1beta.SubscribeProjectEvent:
		if len(inner.EventNames) > 0 {
			return fmt.Sprintf("%s.%s", extensionID, inner.EventNames[0])
		}
	case *v1beta.ProjectHandlerStatus:
		return fmt.Sprintf("%s.%s", extensionID, inner.EventName)
	case *v1beta.InvokeProjectHandler:
		return fmt.Sprintf("%s.%s", extensionID, inner.EventName)
	case *v1beta.SubscribeServiceEvent:
		if len(inner.EventNames) > 0 {
			return fmt.Sprintf("%s.%s", extensionID, inner.EventNames[0])
		}
	case *v1beta.ServiceHandlerStatus:
		return fmt.Sprintf(
			"%s.%s.%s",
			extensionID,
			inner.GetServiceName(),
			inner.GetEventName(),
		)
	case *v1beta.InvokeServiceHandler:
		if inner.Service != nil {
			return fmt.Sprintf(
				"%s.%s.%s",
				extensionID,
				inner.Service.Name,
				inner.EventName,
			)
		}
	default:
	}
	return ""
}

func (e *betaEventMessageEnvelope) SetRequestId(
	_ context.Context,
	msg *v1beta.EventMessage,
	id string,
) {
	msg.RequestId = id
}

func (*betaEventMessageEnvelope) GetError(
	msg *v1beta.EventMessage,
) error {
	if msg == nil || msg.Error == nil {
		return nil
	}
	return unwrapBetaExtensionError(msg.Error)
}

func wrapBetaError(err error) *v1beta.ExtensionError {
	stableError := azdext.WrapError(err)
	if stableError == nil {
		return nil
	}

	betaError := &v1beta.ExtensionError{
		Message:    stableError.GetMessage(),
		Origin:     v1beta.ErrorOrigin(stableError.GetOrigin()),
		Suggestion: stableError.GetSuggestion(),
	}
	for _, link := range stableError.GetLinks() {
		if link != nil {
			betaError.Links = append(betaError.Links, &v1beta.ErrorLink{
				Url:   link.GetUrl(),
				Title: link.GetTitle(),
			})
		}
	}

	if serviceError := stableError.GetServiceError(); serviceError != nil {
		betaError.Source = &v1beta.ExtensionError_ServiceError{
			ServiceError: &v1beta.ServiceErrorDetail{
				ErrorCode:   serviceError.GetErrorCode(),
				StatusCode:  serviceError.GetStatusCode(),
				ServiceName: serviceError.GetServiceName(),
			},
		}
	}
	if localError := stableError.GetLocalError(); localError != nil {
		localErrorDetail := &v1beta.LocalErrorDetail{
			Code:     localError.GetCode(),
			Category: localError.GetCategory(),
		}
		if typedError, ok := errors.AsType[*azdext.LocalError](err); ok {
			localErrorDetail.CauseTypes = errorchain.NormalizeCauseTypes(
				typedError.CauseTypes,
			)
		}
		betaError.Source = &v1beta.ExtensionError_LocalError{
			LocalError: localErrorDetail,
		}
	}
	if toolError, ok := errors.AsType[*azdext.ToolError](err); ok &&
		stableError.GetOrigin() == azdext.ErrorOrigin_ERROR_ORIGIN_TOOL {
		betaError.Origin = v1beta.ErrorOrigin_ERROR_ORIGIN_TOOL
		toolErrorDetail := &v1beta.ToolErrorDetail{
			ToolName:    toolError.ToolName,
			FailureKind: string(toolError.Kind),
		}
		if toolError.ExitCode != nil {
			toolErrorDetail.ExitCode = new(int64(*toolError.ExitCode))
		}
		betaError.Source = &v1beta.ExtensionError_ToolError{
			ToolError: toolErrorDetail,
		}
	}
	return betaError
}

func (e *betaEventMessageEnvelope) SetError(
	msg *v1beta.EventMessage,
	err error,
) {
	msg.Error = wrapBetaError(err)
}

func (*betaEventMessageEnvelope) IsProgressMessage(*v1beta.EventMessage) bool {
	return false
}

func (*betaEventMessageEnvelope) GetProgressMessage(*v1beta.EventMessage) string {
	return ""
}

func (*betaEventMessageEnvelope) CreateProgressMessage(string, string) *v1beta.EventMessage {
	return nil
}

func (*betaEventMessageEnvelope) GetInnerMessage(
	msg *v1beta.EventMessage,
) any {
	if msg == nil {
		return nil
	}

	switch message := msg.MessageType.(type) {
	case *v1beta.EventMessage_SubscribeProjectEvent:
		return message.SubscribeProjectEvent
	case *v1beta.EventMessage_SubscribeServiceEvent:
		return message.SubscribeServiceEvent
	case *v1beta.EventMessage_InvokeProjectHandler:
		return message.InvokeProjectHandler
	case *v1beta.EventMessage_InvokeServiceHandler:
		return message.InvokeServiceHandler
	case *v1beta.EventMessage_ProjectHandlerStatus:
		return message.ProjectHandlerStatus
	case *v1beta.EventMessage_ServiceHandlerStatus:
		return message.ServiceHandlerStatus
	case *v1beta.EventMessage_SubscribeProjectEventResponse:
		return message.SubscribeProjectEventResponse
	case *v1beta.EventMessage_SubscribeServiceEventResponse:
		return message.SubscribeServiceEventResponse
	default:
		return nil
	}
}
