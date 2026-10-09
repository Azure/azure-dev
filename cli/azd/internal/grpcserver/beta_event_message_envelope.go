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
	"google.golang.org/protobuf/proto"
)

type betaEventMessageEnvelope struct{}

var _ grpcbroker.MessageEnvelope[v1beta.EventMessage] = (*betaEventMessageEnvelope)(nil)

func newBetaEventMessageEnvelope() *betaEventMessageEnvelope {
	return &betaEventMessageEnvelope{}
}

func (e *betaEventMessageEnvelope) GetRequestId(
	ctx context.Context, msg *v1beta.EventMessage,
) string {
	if msg != nil && msg.RequestId != "" {
		return msg.RequestId
	}

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
	_ context.Context, msg *v1beta.EventMessage, id string,
) {
	msg.RequestId = id
}

func (*betaEventMessageEnvelope) GetError(msg *v1beta.EventMessage) error {
	if msg == nil || msg.Error == nil {
		return nil
	}

	wire, marshalErr := proto.Marshal(msg.Error)
	if marshalErr == nil {
		stableError := new(azdext.ExtensionError)
		if unmarshalErr := proto.Unmarshal(wire, stableError); unmarshalErr == nil {
			return azdext.UnwrapError(stableError)
		}
	}
	return fmt.Errorf("%s", msg.Error.GetMessage())
}

func wrapBetaError(err error) *v1beta.ExtensionError {
	if err == nil {
		return nil
	}

	stableError := azdext.WrapError(err)
	wire, marshalErr := proto.Marshal(stableError)
	if marshalErr == nil {
		betaError := new(v1beta.ExtensionError)
		if unmarshalErr := proto.Unmarshal(wire, betaError); unmarshalErr == nil {
			if localErr, ok := errors.AsType[*azdext.LocalError](err); ok {
				if betaLocalErr := betaError.GetLocalError(); betaLocalErr != nil {
					betaLocalErr.CauseTypes = errorchain.NormalizeCauseTypes(localErr.CauseTypes)
				}
			}
			if stableError.GetOrigin() == azdext.ErrorOrigin_ERROR_ORIGIN_TOOL {
				if toolErr, ok := errors.AsType[*azdext.ToolError](err); ok {
					var exitCode *int64
					if toolErr.ExitCode != nil {
						exitCode = new(int64(*toolErr.ExitCode))
					}
					betaError.Source = &v1beta.ExtensionError_ToolError{
						ToolError: &v1beta.ToolErrorDetail{
							ToolName:    toolErr.ToolName,
							FailureKind: string(toolErr.Kind),
							ExitCode:    exitCode,
						},
					}
				}
			}
			return betaError
		}
	}

	return &v1beta.ExtensionError{Message: err.Error()}
}

func (*betaEventMessageEnvelope) SetError(
	msg *v1beta.EventMessage, err error,
) {
	msg.Error = wrapBetaError(err)
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
	case *v1beta.EventMessage_SubscribeProjectEventResponse:
		return m.SubscribeProjectEventResponse
	case *v1beta.EventMessage_SubscribeServiceEventResponse:
		return m.SubscribeServiceEventResponse
	default:
		return nil
	}
}
