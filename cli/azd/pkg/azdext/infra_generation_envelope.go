// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// InfraGenerationEnvelope provides broker operations for the beta generation stream.
type InfraGenerationEnvelope struct{}

var _ grpcbroker.MessageEnvelope[v1beta.InfraGenerationMessage] = (*InfraGenerationEnvelope)(nil)

// GetRequestId returns the envelope's correlation ID.
func (e *InfraGenerationEnvelope) GetRequestId(_ context.Context, msg *v1beta.InfraGenerationMessage) string {
	return msg.GetRequestId()
}

// SetRequestId sets the envelope's correlation ID.
func (e *InfraGenerationEnvelope) SetRequestId(_ context.Context, msg *v1beta.InfraGenerationMessage, id string) {
	msg.RequestId = id
}

// GetError decodes a structured generation error.
func (e *InfraGenerationEnvelope) GetError(msg *v1beta.InfraGenerationMessage) error {
	if msg.Error == nil {
		return nil
	}
	stable := &ExtensionError{}
	if err := transcodeServiceTargetMessage(msg.Error, stable); err != nil {
		return fmt.Errorf("decoding generation error: %w", err)
	}
	return UnwrapError(stable)
}

// SetError encodes a structured generation error.
func (e *InfraGenerationEnvelope) SetError(msg *v1beta.InfraGenerationMessage, err error) {
	if err == nil {
		msg.Error = nil
		return
	}
	msg.Error = &v1beta.ExtensionError{}
	if conversionErr := transcodeServiceTargetMessage(WrapError(err), msg.Error); conversionErr != nil {
		msg.Error.Message = fmt.Sprintf("encoding generation error: %v (original error: %v)", conversionErr, err)
	}
}

// GetInnerMessage returns the request or response carried by the envelope.
func (e *InfraGenerationEnvelope) GetInnerMessage(msg *v1beta.InfraGenerationMessage) any {
	switch m := msg.MessageType.(type) {
	case *v1beta.InfraGenerationMessage_RegisterRequest:
		return m.RegisterRequest
	case *v1beta.InfraGenerationMessage_RegisterResponse:
		return m.RegisterResponse
	case *v1beta.InfraGenerationMessage_GenerateRequest:
		return m.GenerateRequest
	case *v1beta.InfraGenerationMessage_GenerateResponse:
		return m.GenerateResponse
	default:
		return nil
	}
}

// IsProgressMessage reports false because generation has no progress messages.
func (e *InfraGenerationEnvelope) IsProgressMessage(_ *v1beta.InfraGenerationMessage) bool {
	return false
}

// GetProgressMessage returns no progress text for this protocol.
func (e *InfraGenerationEnvelope) GetProgressMessage(_ *v1beta.InfraGenerationMessage) string {
	return ""
}

// CreateProgressMessage reports unsupported progress as an explicit protocol error.
func (e *InfraGenerationEnvelope) CreateProgressMessage(id, message string) *v1beta.InfraGenerationMessage {
	msg := &v1beta.InfraGenerationMessage{RequestId: id}
	e.SetError(msg, fmt.Errorf("infrastructure generation does not support progress messages: %s", message))
	return msg
}
