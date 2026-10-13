// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"google.golang.org/protobuf/proto"
)

// ServiceTargetPreviewEnvelope provides broker operations for the experimental v1beta
// deployment preview stream. The stream only carries preview registration and preview messages;
// ordinary service target lifecycle messages stay on the stable stream.
type ServiceTargetPreviewEnvelope struct{}

// NewServiceTargetPreviewEnvelope creates envelope operations for the deployment preview stream.
func NewServiceTargetPreviewEnvelope() *ServiceTargetPreviewEnvelope {
	return &ServiceTargetPreviewEnvelope{}
}

var _ grpcbroker.MessageEnvelope[v1beta.ServiceTargetMessage] = (*ServiceTargetPreviewEnvelope)(nil)

func (ops *ServiceTargetPreviewEnvelope) GetRequestId(ctx context.Context, msg *v1beta.ServiceTargetMessage) string {
	return msg.RequestId
}

func (ops *ServiceTargetPreviewEnvelope) SetRequestId(ctx context.Context, msg *v1beta.ServiceTargetMessage, id string) {
	msg.RequestId = id
}

func (ops *ServiceTargetPreviewEnvelope) GetError(msg *v1beta.ServiceTargetMessage) error {
	if msg.Error == nil {
		return nil
	}

	stableError := &ExtensionError{}
	if err := transcodeServiceTargetMessage(msg.Error, stableError); err != nil {
		return err
	}

	return UnwrapError(stableError)
}

func (ops *ServiceTargetPreviewEnvelope) SetError(msg *v1beta.ServiceTargetMessage, err error) {
	if err == nil {
		msg.Error = nil
		return
	}

	msg.Error = &v1beta.ExtensionError{}
	if transcodeErr := transcodeServiceTargetMessage(WrapError(err), msg.Error); transcodeErr != nil {
		msg.Error = &v1beta.ExtensionError{Message: err.Error()}
	}
}

func (ops *ServiceTargetPreviewEnvelope) GetInnerMessage(msg *v1beta.ServiceTargetMessage) any {
	switch m := msg.MessageType.(type) {
	case *v1beta.ServiceTargetMessage_RegisterServiceTargetRequest:
		return m.RegisterServiceTargetRequest
	case *v1beta.ServiceTargetMessage_RegisterServiceTargetResponse:
		return m.RegisterServiceTargetResponse
	case *v1beta.ServiceTargetMessage_PreviewRequest:
		return m.PreviewRequest
	case *v1beta.ServiceTargetMessage_PreviewResponse:
		return m.PreviewResponse
	case *v1beta.ServiceTargetMessage_ProgressMessage:
		return m.ProgressMessage
	default:
		return nil
	}
}

func (ops *ServiceTargetPreviewEnvelope) IsProgressMessage(msg *v1beta.ServiceTargetMessage) bool {
	return msg.GetProgressMessage() != nil
}

func (ops *ServiceTargetPreviewEnvelope) GetProgressMessage(msg *v1beta.ServiceTargetMessage) string {
	return msg.GetProgressMessage().GetMessage()
}

func (ops *ServiceTargetPreviewEnvelope) CreateProgressMessage(
	requestId string,
	message string,
) *v1beta.ServiceTargetMessage {
	return &v1beta.ServiceTargetMessage{
		RequestId: requestId,
		MessageType: &v1beta.ServiceTargetMessage_ProgressMessage{
			ProgressMessage: &v1beta.ServiceTargetProgressMessage{RequestId: requestId, Message: message},
		},
	}
}

// transcodeServiceTargetMessage converts between wire-compatible stable and v1beta messages.
func transcodeServiceTargetMessage(source, destination proto.Message) error {
	wire, err := proto.Marshal(source)
	if err != nil {
		return err
	}

	return proto.Unmarshal(wire, destination)
}
