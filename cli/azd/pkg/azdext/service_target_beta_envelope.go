// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// BetaServiceTargetEnvelope provides broker operations for beta service target messages.
type BetaServiceTargetEnvelope struct{}

// NewBetaServiceTargetEnvelope creates beta service target envelope operations.
func NewBetaServiceTargetEnvelope() *BetaServiceTargetEnvelope {
	return &BetaServiceTargetEnvelope{}
}

var _ grpcbroker.MessageEnvelope[v1beta.ServiceTargetMessage] = (*BetaServiceTargetEnvelope)(nil)

func (ops *BetaServiceTargetEnvelope) GetRequestId(
	ctx context.Context,
	message *v1beta.ServiceTargetMessage,
) string {
	return message.RequestId
}

func (ops *BetaServiceTargetEnvelope) SetRequestId(
	ctx context.Context,
	message *v1beta.ServiceTargetMessage,
	id string,
) {
	message.RequestId = id
}

func (ops *BetaServiceTargetEnvelope) GetError(message *v1beta.ServiceTargetMessage) error {
	if message.Error == nil {
		return nil
	}
	stableError := new(ExtensionError)
	if err := convertServiceTargetMessage(message.Error, stableError); err != nil {
		return err
	}
	return UnwrapError(stableError)
}

func (ops *BetaServiceTargetEnvelope) SetError(message *v1beta.ServiceTargetMessage, err error) {
	if err == nil {
		message.Error = nil
		return
	}
	stableError := WrapError(err)
	message.Error = new(v1beta.ExtensionError)
	if conversionErr := convertServiceTargetMessage(stableError, message.Error); conversionErr != nil {
		message.Error = &v1beta.ExtensionError{Message: conversionErr.Error()}
	}
}

func (ops *BetaServiceTargetEnvelope) GetInnerMessage(message *v1beta.ServiceTargetMessage) any {
	switch value := message.MessageType.(type) {
	case *v1beta.ServiceTargetMessage_RegisterServiceTargetRequest:
		return value.RegisterServiceTargetRequest
	case *v1beta.ServiceTargetMessage_RegisterServiceTargetResponse:
		return value.RegisterServiceTargetResponse
	case *v1beta.ServiceTargetMessage_InitializeRequest:
		return value.InitializeRequest
	case *v1beta.ServiceTargetMessage_InitializeResponse:
		return value.InitializeResponse
	case *v1beta.ServiceTargetMessage_GetTargetResourceRequest:
		return value.GetTargetResourceRequest
	case *v1beta.ServiceTargetMessage_GetTargetResourceResponse:
		return value.GetTargetResourceResponse
	case *v1beta.ServiceTargetMessage_DeployRequest:
		return value.DeployRequest
	case *v1beta.ServiceTargetMessage_DeployResponse:
		return value.DeployResponse
	case *v1beta.ServiceTargetMessage_PreviewRequest:
		return value.PreviewRequest
	case *v1beta.ServiceTargetMessage_PreviewResponse:
		return value.PreviewResponse
	case *v1beta.ServiceTargetMessage_ProgressMessage:
		return value.ProgressMessage
	case *v1beta.ServiceTargetMessage_PackageRequest:
		return value.PackageRequest
	case *v1beta.ServiceTargetMessage_PackageResponse:
		return value.PackageResponse
	case *v1beta.ServiceTargetMessage_PublishRequest:
		return value.PublishRequest
	case *v1beta.ServiceTargetMessage_PublishResponse:
		return value.PublishResponse
	case *v1beta.ServiceTargetMessage_EndpointsRequest:
		return value.EndpointsRequest
	case *v1beta.ServiceTargetMessage_EndpointsResponse:
		return value.EndpointsResponse
	default:
		return nil
	}
}

func (ops *BetaServiceTargetEnvelope) IsProgressMessage(message *v1beta.ServiceTargetMessage) bool {
	return message.GetProgressMessage() != nil
}

func (ops *BetaServiceTargetEnvelope) GetProgressMessage(message *v1beta.ServiceTargetMessage) string {
	if progress := message.GetProgressMessage(); progress != nil {
		return progress.GetMessage()
	}
	return ""
}

func (ops *BetaServiceTargetEnvelope) CreateProgressMessage(
	requestID string,
	message string,
) *v1beta.ServiceTargetMessage {
	return &v1beta.ServiceTargetMessage{
		RequestId: requestID,
		MessageType: &v1beta.ServiceTargetMessage_ProgressMessage{
			ProgressMessage: &v1beta.ServiceTargetProgressMessage{
				RequestId: requestID,
				Message:   message,
			},
		},
	}
}
