// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/errorchain"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type betaEventMessageEnvelope struct{}

var _ grpcbroker.MessageEnvelope[v1beta.EventMessage] = betaEventMessageEnvelope{}

func (betaEventMessageEnvelope) GetRequestId(
	_ context.Context,
	msg *v1beta.EventMessage,
) string {
	return msg.GetRequestId()
}

func (betaEventMessageEnvelope) SetRequestId(
	_ context.Context,
	msg *v1beta.EventMessage,
	id string,
) {
	msg.RequestId = id
}

func (betaEventMessageEnvelope) GetError(msg *v1beta.EventMessage) error {
	if msg.GetError() == nil {
		return nil
	}
	return unwrapBetaExtensionError(msg.GetError())
}

func (betaEventMessageEnvelope) SetError(msg *v1beta.EventMessage, err error) {
	msg.Error = wrapBetaEventError(err)
}

func (betaEventMessageEnvelope) GetInnerMessage(msg *v1beta.EventMessage) any {
	switch message := msg.GetMessageType().(type) {
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
	case *v1beta.EventMessage_SubscribeProjectEventResponse:
		return message.SubscribeProjectEventResponse
	case *v1beta.EventMessage_SubscribeServiceEventResponse:
		return message.SubscribeServiceEventResponse
	case *v1beta.EventMessage_HandlerOutput:
		return message.HandlerOutput
	default:
		return nil
	}
}

func (betaEventMessageEnvelope) IsProgressMessage(msg *v1beta.EventMessage) bool {
	return msg.GetHandlerOutput() != nil
}

func (betaEventMessageEnvelope) GetProgressMessage(msg *v1beta.EventMessage) string {
	return msg.GetHandlerOutput().GetOutput()
}

func (betaEventMessageEnvelope) CreateProgressMessage(
	requestID string,
	message string,
) *v1beta.EventMessage {
	return &v1beta.EventMessage{
		RequestId: requestID,
		MessageType: &v1beta.EventMessage_HandlerOutput{
			HandlerOutput: &v1beta.HandlerOutput{Output: message},
		},
	}
}

func wrapBetaEventError(err error) *v1beta.ExtensionError {
	if err == nil {
		return nil
	}

	stableError := azdext.WrapError(err)
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

	switch source := stableError.GetSource().(type) {
	case *azdext.ExtensionError_ServiceError:
		if detail := source.ServiceError; detail != nil {
			betaError.Source = &v1beta.ExtensionError_ServiceError{
				ServiceError: &v1beta.ServiceErrorDetail{
					ErrorCode:   detail.GetErrorCode(),
					StatusCode:  detail.GetStatusCode(),
					ServiceName: detail.GetServiceName(),
				},
			}
		}
	case *azdext.ExtensionError_LocalError:
		if detail := source.LocalError; detail != nil {
			betaDetail := &v1beta.LocalErrorDetail{
				Code:     detail.GetCode(),
				Category: detail.GetCategory(),
			}
			if localErr, ok := errors.AsType[*azdext.LocalError](err); ok {
				betaDetail.CauseTypes = errorchain.NormalizeCauseTypes(localErr.CauseTypes)
			}
			betaError.Source = &v1beta.ExtensionError_LocalError{
				LocalError: betaDetail,
			}
		}
	}

	if toolErr, ok := errors.AsType[*azdext.ToolError](err); ok &&
		stableError.GetOrigin() == azdext.ErrorOrigin_ERROR_ORIGIN_TOOL {
		betaError.Origin = v1beta.ErrorOrigin_ERROR_ORIGIN_TOOL
		detail := &v1beta.ToolErrorDetail{
			ToolName:    toolErr.ToolName,
			FailureKind: string(toolErr.Kind),
		}
		if toolErr.ExitCode != nil {
			detail.ExitCode = new(int64(*toolErr.ExitCode))
		}
		betaError.Source = &v1beta.ExtensionError_ToolError{ToolError: detail}
	}

	return betaError
}
