// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type addProviderRegistration struct {
	items   []*v1beta.AddItem
	handler func(context.Context, string) error
}

type addProviderReceiver struct {
	*grpcbroker.MessageBroker[v1beta.AddProviderMessage]
}

func (r addProviderReceiver) Receive(ctx context.Context) error {
	return r.Run(ctx)
}

// WithBetaAddProvider advertises add items while Run is active. The handler
// receives the selected item ID and a context scoped to that invocation.
// An empty item list is valid. Calling this method again replaces the registration.
func (er *ExtensionHost) WithBetaAddProvider(
	items []*v1beta.AddItem, handler func(context.Context, string) error,
) *ExtensionHost {
	er.addProvider = &addProviderRegistration{handler: handler, items: make([]*v1beta.AddItem, len(items))}
	for i, item := range items {
		er.addProvider.items[i] = proto.CloneOf(item)
	}
	return er
}

func (r *addProviderRegistration) connect(
	ctx context.Context,
	client v1beta.AddProviderServiceClient,
	id string,
	logger *log.Logger,
) (*grpcbroker.MessageBroker[v1beta.AddProviderMessage], error) {
	if r.handler == nil {
		return nil, errors.New("add provider requires a handler")
	}
	stream, err := client.Stream(ctx)
	if err != nil {
		return nil, err
	}
	requestID := uuid.NewString()
	err = stream.Send(&v1beta.AddProviderMessage{
		RequestId: requestID,
		MessageType: &v1beta.AddProviderMessage_RegisterRequest{
			RegisterRequest: &v1beta.RegisterAddProviderRequest{Items: r.items},
		},
	})
	// Recv exposes the server status even if Send got EOF due to a rejected stream.
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if response.GetRequestId() != requestID || response.GetRegisterResponse() == nil || response.GetError() != nil {
		return nil, errors.New("expected a correlated add provider registration response")
	}

	broker := grpcbroker.NewMessageBroker(stream, &AddProviderEnvelope{}, id, logger)
	items := make(map[string]bool, len(r.items))
	for _, item := range r.items {
		items[item.GetId()] = true
	}
	err = broker.On(func(ctx context.Context, request *v1beta.AddItemRequest) (*v1beta.AddProviderMessage, error) {
		if !items[request.GetId()] {
			return nil, status.Errorf(codes.InvalidArgument, "unknown add item %q", request.GetId())
		}
		if err := r.handler(ctx, request.GetId()); err != nil {
			return nil, err
		}

		return &v1beta.AddProviderMessage{
			MessageType: &v1beta.AddProviderMessage_AddResponse{AddResponse: &v1beta.AddItemResponse{}},
		}, nil
	})
	if err != nil {
		broker.Close()
		return nil, fmt.Errorf("installing add provider handler: %w", err)
	}
	return broker, nil
}

// AddProviderEnvelope adapts the beta add protocol to the shared stream broker.
type AddProviderEnvelope struct{}

func (*AddProviderEnvelope) GetRequestId(_ context.Context, msg *v1beta.AddProviderMessage) string {
	return msg.GetRequestId()
}

func (*AddProviderEnvelope) SetRequestId(_ context.Context, msg *v1beta.AddProviderMessage, id string) {
	msg.RequestId = id
}

func (*AddProviderEnvelope) GetError(msg *v1beta.AddProviderMessage) error {
	if msg.GetError() == nil {
		return nil
	}
	stable := &ExtensionError{}
	if err := transcodeServiceTargetMessage(msg.GetError(), stable); err != nil {
		return fmt.Errorf("decoding add provider error: %w", err)
	}
	return UnwrapError(stable)
}

func (*AddProviderEnvelope) SetError(msg *v1beta.AddProviderMessage, err error) {
	if err == nil {
		msg.Error = nil
		return
	}
	msg.Error = &v1beta.ExtensionError{}
	if conversionErr := transcodeServiceTargetMessage(WrapError(err), msg.Error); conversionErr != nil {
		msg.Error.Message = err.Error()
	}
}

func (*AddProviderEnvelope) GetInnerMessage(msg *v1beta.AddProviderMessage) any {
	switch m := msg.GetMessageType().(type) {
	case *v1beta.AddProviderMessage_AddRequest:
		return m.AddRequest
	case *v1beta.AddProviderMessage_AddResponse:
		return m.AddResponse
	default:
		return nil
	}
}

func (*AddProviderEnvelope) IsProgressMessage(*v1beta.AddProviderMessage) bool { return false }

func (*AddProviderEnvelope) GetProgressMessage(*v1beta.AddProviderMessage) string { return "" }

func (e *AddProviderEnvelope) CreateProgressMessage(id, message string) *v1beta.AddProviderMessage {
	msg := &v1beta.AddProviderMessage{RequestId: id}
	e.SetError(msg, fmt.Errorf("add provider does not support progress messages: %s", message))
	return msg
}

func (e *AddProviderEnvelope) CreateCancellationMessage(
	_ context.Context, request *v1beta.AddProviderMessage, err error,
) *v1beta.AddProviderMessage {
	msg := &v1beta.AddProviderMessage{
		RequestId:   request.GetRequestId(),
		MessageType: &v1beta.AddProviderMessage_Cancel{Cancel: &v1beta.AddItemCancel{}},
	}
	e.SetError(msg, err)
	return msg
}

func (*AddProviderEnvelope) IsCancellationMessage(_ context.Context, msg *v1beta.AddProviderMessage) bool {
	return msg.GetCancel() != nil
}

func (e *AddProviderEnvelope) GetCancellationError(msg *v1beta.AddProviderMessage) error {
	if err := e.GetError(msg); err != nil {
		return err
	}
	return context.Canceled
}
