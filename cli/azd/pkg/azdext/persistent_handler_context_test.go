// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/stretchr/testify/require"
)

func preservesHandlerContext[T any](
	ctx context.Context,
	envelope grpcbroker.MessageEnvelope[T],
	message *T,
) bool {
	persistentEnvelope, ok := envelope.(grpcbroker.PersistentHandlerContextEnvelope[T])
	return ok && persistentEnvelope.PreserveHandlerContext(ctx, message)
}

func TestOnlyEventSubscriptionsPreserveHandlerContext(t *testing.T) {
	t.Run("event", func(t *testing.T) {
		envelope := NewEventMessageEnvelopeWithExtensionId("test.extension")
		require.True(t, preservesHandlerContext(t.Context(), envelope, &EventMessage{
			MessageType: &EventMessage_SubscribeProjectEvent{
				SubscribeProjectEvent: &SubscribeProjectEvent{EventNames: []string{"prepackage"}},
			},
		}))
		require.False(t, preservesHandlerContext(t.Context(), envelope, &EventMessage{
			MessageType: &EventMessage_InvokeProjectHandler{
				InvokeProjectHandler: &InvokeProjectHandler{EventName: "prepackage"},
			},
		}))
	})

	t.Run("service target", func(t *testing.T) {
		envelope := NewServiceTargetEnvelope()
		require.False(t, preservesHandlerContext(t.Context(), envelope, &ServiceTargetMessage{
			MessageType: &ServiceTargetMessage_RegisterServiceTargetRequest{
				RegisterServiceTargetRequest: &RegisterServiceTargetRequest{},
			},
		}))
		require.False(t, preservesHandlerContext(t.Context(), envelope, &ServiceTargetMessage{
			MessageType: &ServiceTargetMessage_InitializeRequest{
				InitializeRequest: &ServiceTargetInitializeRequest{},
			},
		}))
		require.False(t, preservesHandlerContext(t.Context(), envelope, &ServiceTargetMessage{
			MessageType: &ServiceTargetMessage_PreviewRequest{
				PreviewRequest: &ServiceTargetPreviewRequest{},
			},
		}))
	})

	t.Run("beta service target preview", func(t *testing.T) {
		envelope := NewServiceTargetPreviewEnvelope()
		require.False(t, preservesHandlerContext(t.Context(), envelope, &v1beta.ServiceTargetMessage{
			MessageType: &v1beta.ServiceTargetMessage_RegisterServiceTargetRequest{
				RegisterServiceTargetRequest: &v1beta.RegisterServiceTargetRequest{},
			},
		}))
		require.False(t, preservesHandlerContext(t.Context(), envelope, &v1beta.ServiceTargetMessage{
			MessageType: &v1beta.ServiceTargetMessage_PreviewRequest{
				//nolint:staticcheck // Exercise the retained beta preview envelope.
				PreviewRequest: &v1beta.ServiceTargetPreviewRequest{},
			},
		}))
	})

	t.Run("framework service", func(t *testing.T) {
		envelope := NewFrameworkServiceEnvelope()
		require.False(t, preservesHandlerContext(t.Context(), envelope, &FrameworkServiceMessage{
			MessageType: &FrameworkServiceMessage_RegisterFrameworkServiceRequest{
				RegisterFrameworkServiceRequest: &RegisterFrameworkServiceRequest{},
			},
		}))
		require.False(t, preservesHandlerContext(t.Context(), envelope, &FrameworkServiceMessage{
			MessageType: &FrameworkServiceMessage_InitializeRequest{
				InitializeRequest: &FrameworkServiceInitializeRequest{},
			},
		}))
	})

	t.Run("provisioning", func(t *testing.T) {
		envelope := NewProvisioningEnvelope()
		require.False(t, preservesHandlerContext(t.Context(), envelope, &ProvisioningMessage{
			MessageType: &ProvisioningMessage_RegisterProvisioningProviderRequest{
				RegisterProvisioningProviderRequest: &RegisterProvisioningProviderRequest{},
			},
		}))
		require.False(t, preservesHandlerContext(t.Context(), envelope, &ProvisioningMessage{
			MessageType: &ProvisioningMessage_InitializeRequest{
				InitializeRequest: &ProvisioningInitializeRequest{},
			},
		}))
	})

	t.Run("validation", func(t *testing.T) {
		envelope := NewValidationEnvelope()
		require.False(t, preservesHandlerContext(t.Context(), envelope, &ValidationMessage{
			MessageType: &ValidationMessage_RegisterValidationCheckRequest{
				RegisterValidationCheckRequest: &RegisterValidationCheckRequest{},
			},
		}))
		require.False(t, preservesHandlerContext(t.Context(), envelope, &ValidationMessage{
			MessageType: &ValidationMessage_ValidationCheckRequest{
				ValidationCheckRequest: &ValidationCheckRequest{},
			},
		}))
	})
}
