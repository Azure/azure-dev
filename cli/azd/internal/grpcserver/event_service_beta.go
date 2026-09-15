// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type betaEventServiceOverride struct {
	service *eventService
}

var _ BetaEventServiceEventStreamOverride = (*betaEventServiceOverride)(nil)

func (s *eventService) betaEventServiceOverride() any {
	return &betaEventServiceOverride{service: s}
}

func (o *betaEventServiceOverride) EventStream(
	stream grpc.BidiStreamingServer[v1beta.EventMessage, v1beta.EventMessage],
) error {
	return o.service.eventStream(
		&versionedBidiServerStream[
			azdext.EventMessage,
			azdext.EventMessage,
			v1beta.EventMessage,
			v1beta.EventMessage,
		]{
			ServerStream: stream,
			beta:         stream,
			operation:    "azd.extensions.v1beta.EventService/EventStream",
			requestToStable: func(request *v1beta.EventMessage) (*azdext.EventMessage, error) {
				stableRequest := new(azdext.EventMessage)
				if err := transcodeBetaStreamRequest(request, stableRequest); err != nil {
					return nil, err
				}
				return stableRequest, nil
			},
			responseToBeta: func(response *azdext.EventMessage) (*v1beta.EventMessage, error) {
				betaResponse := new(v1beta.EventMessage)
				if err := transcodeStableResponse(response, betaResponse); err != nil {
					return nil, err
				}
				return betaResponse, nil
			},
		},
		newBetaBridgeEventEnvelope(),
	)
}

type betaBridgeEventEnvelope struct {
	stable *azdext.EventMessageEnvelope
}

var _ grpcbroker.MessageEnvelope[azdext.EventMessage] = (*betaBridgeEventEnvelope)(nil)

func newBetaBridgeEventEnvelope() *betaBridgeEventEnvelope {
	return &betaBridgeEventEnvelope{stable: azdext.NewEventMessageEnvelope()}
}

func (e *betaBridgeEventEnvelope) GetRequestId(ctx context.Context, msg *azdext.EventMessage) string {
	if output := betaHandlerOutput(msg); output != nil {
		return output.RequestId
	}
	return e.stable.GetRequestId(ctx, msg)
}

func (e *betaBridgeEventEnvelope) SetRequestId(
	ctx context.Context,
	msg *azdext.EventMessage,
	id string,
) {
	e.stable.SetRequestId(ctx, msg, id)
}

func (e *betaBridgeEventEnvelope) GetError(msg *azdext.EventMessage) error {
	return e.stable.GetError(msg)
}

func (e *betaBridgeEventEnvelope) SetError(msg *azdext.EventMessage, err error) {
	e.stable.SetError(msg, err)
}

func (e *betaBridgeEventEnvelope) GetInnerMessage(msg *azdext.EventMessage) any {
	if output := betaHandlerOutput(msg); output != nil {
		return output
	}
	return e.stable.GetInnerMessage(msg)
}

func (e *betaBridgeEventEnvelope) IsProgressMessage(msg *azdext.EventMessage) bool {
	return betaHandlerOutput(msg) != nil
}

func (e *betaBridgeEventEnvelope) GetProgressMessage(msg *azdext.EventMessage) string {
	if output := betaHandlerOutput(msg); output != nil {
		return output.Output
	}
	return ""
}

func (e *betaBridgeEventEnvelope) CreateProgressMessage(requestID string, message string) *azdext.EventMessage {
	betaMessage := &v1beta.EventMessage{
		MessageType: &v1beta.EventMessage_HandlerOutput{
			HandlerOutput: &v1beta.HandlerOutput{
				RequestId: requestID,
				Output:    message,
			},
		},
	}
	stableMessage := new(azdext.EventMessage)
	if err := transcodeEventMessage(betaMessage, stableMessage); err != nil {
		return nil
	}
	return stableMessage
}

func betaHandlerOutput(msg *azdext.EventMessage) *v1beta.HandlerOutput {
	betaMessage := new(v1beta.EventMessage)
	if err := transcodeEventMessage(msg, betaMessage); err != nil {
		return nil
	}
	return betaMessage.GetHandlerOutput()
}

func transcodeEventMessage(source, destination proto.Message) error {
	wire, err := proto.Marshal(source)
	if err != nil {
		return fmt.Errorf("marshal event message: %w", err)
	}
	if err := proto.Unmarshal(wire, destination); err != nil {
		return fmt.Errorf("unmarshal event message: %w", err)
	}
	return nil
}
