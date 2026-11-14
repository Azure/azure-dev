// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// BetaInputEnvelope provides correlation, cancellation, and structured errors for input streams.
type BetaInputEnvelope struct{}

// NewBetaInputEnvelope creates the envelope used by both sides of the preview input protocol.
func NewBetaInputEnvelope() *BetaInputEnvelope {
	return &BetaInputEnvelope{}
}

var _ grpcbroker.MessageEnvelope[v1beta.InputMessage] = (*BetaInputEnvelope)(nil)

// GetRequestId returns the message correlation ID.
func (*BetaInputEnvelope) GetRequestId(_ context.Context, message *v1beta.InputMessage) string {
	return message.GetRequestId()
}

// SetRequestId sets the message correlation ID.
func (*BetaInputEnvelope) SetRequestId(_ context.Context, message *v1beta.InputMessage, id string) {
	message.RequestId = id
}

// GetError decodes an extension error without discarding preview details.
func (*BetaInputEnvelope) GetError(message *v1beta.InputMessage) error {
	if message.Error == nil {
		return nil
	}
	stableError := new(ExtensionError)
	if err := transcodeBetaMessage(message.Error, stableError); err != nil {
		return fmt.Errorf("decoding input provider error: %w", err)
	}
	return UnwrapError(stableError)
}

// SetError encodes a structured extension error.
func (*BetaInputEnvelope) SetError(message *v1beta.InputMessage, err error) {
	message.Error = wrapBetaExtensionError(err)
}

// GetInnerMessage returns the request or response payload.
func (*BetaInputEnvelope) GetInnerMessage(message *v1beta.InputMessage) any {
	switch payload := message.MessageType.(type) {
	case *v1beta.InputMessage_RegisterInputProviderRequest:
		return payload.RegisterInputProviderRequest
	case *v1beta.InputMessage_RegisterInputProviderResponse:
		return payload.RegisterInputProviderResponse
	case *v1beta.InputMessage_ResolveInputRequest:
		return payload.ResolveInputRequest
	case *v1beta.InputMessage_ResolveInputResponse:
		return payload.ResolveInputResponse
	default:
		return nil
	}
}

// IsProgressMessage reports whether a message carries progress. Inputs have no progress messages.
func (*BetaInputEnvelope) IsProgressMessage(*v1beta.InputMessage) bool { return false }

// GetProgressMessage returns no progress text.
func (*BetaInputEnvelope) GetProgressMessage(*v1beta.InputMessage) string { return "" }

// CreateProgressMessage returns an empty correlated envelope.
func (*BetaInputEnvelope) CreateProgressMessage(id string, _ string) *v1beta.InputMessage {
	return &v1beta.InputMessage{RequestId: id}
}
