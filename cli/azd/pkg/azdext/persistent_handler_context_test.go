// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/require"
)

func TestRegistrationMessagesPreserveHandlerContext(t *testing.T) {
	t.Run("event", func(t *testing.T) {
		envelope := NewEventMessageEnvelope("test.extension")
		require.True(t, envelope.PreserveHandlerContext(t.Context(), &EventMessage{
			MessageType: &EventMessage_SubscribeProjectEvent{
				SubscribeProjectEvent: &SubscribeProjectEvent{EventNames: []string{"prepackage"}},
			},
		}))
		require.False(t, envelope.PreserveHandlerContext(t.Context(), &EventMessage{
			MessageType: &EventMessage_InvokeProjectHandler{
				InvokeProjectHandler: &InvokeProjectHandler{EventName: "prepackage"},
			},
		}))
	})

	t.Run("service target", func(t *testing.T) {
		envelope := NewServiceTargetEnvelope()
		require.True(t, envelope.PreserveHandlerContext(t.Context(), &ServiceTargetMessage{
			MessageType: &ServiceTargetMessage_RegisterServiceTargetRequest{
				RegisterServiceTargetRequest: &RegisterServiceTargetRequest{},
			},
		}))
		require.False(t, envelope.PreserveHandlerContext(t.Context(), &ServiceTargetMessage{
			MessageType: &ServiceTargetMessage_InitializeRequest{
				InitializeRequest: &ServiceTargetInitializeRequest{},
			},
		}))
	})

	t.Run("service target preview", func(t *testing.T) {
		envelope := NewServiceTargetPreviewEnvelope()
		require.True(t, envelope.PreserveHandlerContext(t.Context(), &v1beta.ServiceTargetMessage{
			MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetRequest{
				RegisterServiceTargetRequest: &v1beta.RegisterServiceTargetRequest{},
			},
		}))
		require.False(t, envelope.PreserveHandlerContext(t.Context(), &v1beta.ServiceTargetMessage{
			MessageType: &v1beta.ServiceTargetMessage_PreviewRequest{
				PreviewRequest: &v1beta.ServiceTargetPreviewRequest{},
			},
		}))
	})

	t.Run("framework service", func(t *testing.T) {
		envelope := NewFrameworkServiceEnvelope()
		require.True(t, envelope.PreserveHandlerContext(t.Context(), &FrameworkServiceMessage{
			MessageType: &FrameworkServiceMessage_RegisterFrameworkServiceRequest{
				RegisterFrameworkServiceRequest: &RegisterFrameworkServiceRequest{},
			},
		}))
		require.False(t, envelope.PreserveHandlerContext(t.Context(), &FrameworkServiceMessage{
			MessageType: &FrameworkServiceMessage_InitializeRequest{
				InitializeRequest: &FrameworkServiceInitializeRequest{},
			},
		}))
	})

	t.Run("provisioning", func(t *testing.T) {
		envelope := NewProvisioningEnvelope()
		require.True(t, envelope.PreserveHandlerContext(t.Context(), &ProvisioningMessage{
			MessageType: &ProvisioningMessage_RegisterProvisioningProviderRequest{
				RegisterProvisioningProviderRequest: &RegisterProvisioningProviderRequest{},
			},
		}))
		require.False(t, envelope.PreserveHandlerContext(t.Context(), &ProvisioningMessage{
			MessageType: &ProvisioningMessage_InitializeRequest{
				InitializeRequest: &ProvisioningInitializeRequest{},
			},
		}))
	})

	t.Run("validation", func(t *testing.T) {
		envelope := NewValidationEnvelope()
		require.True(t, envelope.PreserveHandlerContext(t.Context(), &ValidationMessage{
			MessageType: &ValidationMessage_RegisterValidationCheckRequest{
				RegisterValidationCheckRequest: &RegisterValidationCheckRequest{},
			},
		}))
		require.False(t, envelope.PreserveHandlerContext(t.Context(), &ValidationMessage{
			MessageType: &ValidationMessage_ValidationCheckRequest{
				ValidationCheckRequest: &ValidationCheckRequest{},
			},
		}))
	})
}
