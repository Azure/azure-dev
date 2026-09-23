// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

// Test the edge case of empty kind

// Test edge cases

func Test_BuiltInServiceTargetNames(t *testing.T) {
	names := builtInServiceTargetNames()
	require.NotEmpty(t, names)

	assert.Contains(t, names, "appservice")
	assert.Contains(t, names, "containerapp")
	assert.Contains(t, names, "function")
	assert.Contains(t, names, "staticwebapp")
	assert.Contains(t, names, "aks")
	assert.Contains(t, names, "ai.endpoint")
}

func Test_ParseServiceHost(t *testing.T) {
	t.Run("valid kinds", func(t *testing.T) {
		kinds := []ServiceTargetKind{
			AppServiceTarget, ContainerAppTarget, AzureFunctionTarget,
			StaticWebAppTarget, AksTarget, AiEndpointTarget,
		}
		for _, kind := range kinds {
			result, err := parseServiceHost(kind)
			require.NoError(t, err)
			assert.Equal(t, kind, result)
		}
	})

	t.Run("empty host returns error", func(t *testing.T) {
		_, err := parseServiceHost(ServiceTargetKind(""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "host cannot be empty")
	})

	t.Run("custom/extension host allowed", func(t *testing.T) {
		result, err := parseServiceHost(ServiceTargetKind("custom-extension"))
		require.NoError(t, err)
		assert.Equal(t, ServiceTargetKind("custom-extension"), result)
	})
}

func Test_ResourceTypeMismatchError(t *testing.T) {
	err := resourceTypeMismatchError("myResource", "Microsoft.Web/sites", "Microsoft.App/containerApps")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "myResource")
	assert.Contains(t, err.Error(), "Microsoft.Web/sites")
	assert.Contains(t, err.Error(), "Microsoft.App/containerApps")
}

func Test_CheckResourceType(t *testing.T) {
	t.Run("matching type", func(t *testing.T) {
		resource := environment.NewTargetResource("sub", "rg", "myApp", "Microsoft.Web/sites")
		err := checkResourceType(resource, "Microsoft.Web/sites")
		require.NoError(t, err)
	})

	t.Run("case insensitive match", func(t *testing.T) {
		resource := environment.NewTargetResource("sub", "rg", "myApp", "microsoft.web/sites")
		err := checkResourceType(resource, "Microsoft.Web/sites")
		require.NoError(t, err)
	})

	t.Run("mismatched type", func(t *testing.T) {
		resource := environment.NewTargetResource("sub", "rg", "myApp", "Microsoft.Web/sites")
		err := checkResourceType(resource, "Microsoft.App/containerApps")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match")
	})
}

func Test_NewExternalServiceTarget(t *testing.T) {
	target := NewExternalServiceTarget("test-target", ContainerAppTarget, nil, nil, nil, nil, nil)
	require.NotNil(t, target)
}

func TestExternalServiceTargetWrapInvocationError(t *testing.T) {
	t.Parallel()

	inner := errors.New("extension failed")
	target := &ExternalServiceTarget{
		extension: &extensions.Extension{Id: "test.extension", Version: "1.2.3"},
	}

	err := target.wrapInvocationError(inner, "deploy")
	require.ErrorIs(t, err, inner)
	metadata, ok := errors.AsType[extensions.InvocationMetadataProvider](err)
	require.True(t, ok)
	require.Equal(t, "test.extension", metadata.InvocationExtensionId())
	require.Equal(t, "1.2.3", metadata.InvocationExtensionVersion())
	require.Equal(t, "service_target.deploy", metadata.InvocationEvent())
}

type scriptedServiceTargetStream struct {
	recvCh  chan *azdext.ServiceTargetMessage
	respond func(*azdext.ServiceTargetMessage) *azdext.ServiceTargetMessage
}

func (s *scriptedServiceTargetStream) Send(msg *azdext.ServiceTargetMessage) error {
	if response := s.respond(msg); response != nil {
		s.recvCh <- response
	}

	return nil
}

func (s *scriptedServiceTargetStream) Recv() (*azdext.ServiceTargetMessage, error) {
	msg, ok := <-s.recvCh
	if !ok {
		return nil, io.EOF
	}

	return msg, nil
}

func newServiceTargetTestBroker(
	t *testing.T,
	respond func(*azdext.ServiceTargetMessage) *azdext.ServiceTargetMessage,
) *grpcbroker.MessageBroker[azdext.ServiceTargetMessage] {
	t.Helper()

	stream := &scriptedServiceTargetStream{
		recvCh:  make(chan *azdext.ServiceTargetMessage, 1),
		respond: respond,
	}
	brokerCtx, cancel := context.WithCancel(t.Context())
	broker := grpcbroker.NewMessageBroker(stream, azdext.NewServiceTargetEnvelope(), "test", nil)
	brokerDone := make(chan struct{})

	t.Cleanup(func() {
		close(stream.recvCh)
		cancel()
		<-brokerDone
	})

	go func() {
		defer close(brokerDone)
		_ = broker.Run(brokerCtx)
	}()

	require.NoError(t, broker.Ready(t.Context()))

	return broker
}

func TestExternalServiceTargetMalformedResponse(t *testing.T) {
	tests := []struct {
		name      string
		respond   func(*azdext.ServiceTargetMessage) *azdext.ServiceTargetMessage
		invoke    func(context.Context, *ExternalServiceTarget) (bool, error)
		operation string
		detail    string
		event     string
	}{
		{
			name: "deploy missing result",
			respond: func(request *azdext.ServiceTargetMessage) *azdext.ServiceTargetMessage {
				return &azdext.ServiceTargetMessage{
					RequestId: request.RequestId,
					MessageType: &azdext.ServiceTargetMessage_DeployResponse{
						DeployResponse: &azdext.ServiceTargetDeployResponse{},
					},
				}
			},
			invoke: func(ctx context.Context, target *ExternalServiceTarget) (bool, error) {
				result, err := target.Deploy(
					ctx,
					&ServiceConfig{Name: "api", Host: ContainerAppTarget},
					NewServiceContext(),
					environment.NewTargetResource(
						"sub", "rg", "api", "Microsoft.App/containerApps"),
					nil,
				)
				return result == nil, err
			},
			operation: "deploy",
			detail:    "missing deploy result",
			event:     "service_target.deploy",
		},
		{
			name: "target resource missing",
			respond: func(request *azdext.ServiceTargetMessage) *azdext.ServiceTargetMessage {
				return &azdext.ServiceTargetMessage{
					RequestId: request.RequestId,
					MessageType: &azdext.ServiceTargetMessage_GetTargetResourceResponse{
						GetTargetResourceResponse: &azdext.GetTargetResourceResponse{},
					},
				}
			},
			invoke: func(ctx context.Context, target *ExternalServiceTarget) (bool, error) {
				result, err := target.ResolveTargetResource(
					ctx,
					"sub",
					&ServiceConfig{Name: "api", Host: ContainerAppTarget},
					nil,
				)
				return result == nil, err
			},
			operation: "get target resource",
			detail:    "missing target resource",
			event:     "service_target.get_target_resource",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			broker := newServiceTargetTestBroker(t, tt.respond)
			target, ok := NewExternalServiceTarget(
				"test-target",
				ContainerAppTarget,
				&extensions.Extension{Id: "test.extension", Version: "1.2.3"},
				broker,
				nil,
				nil,
				nil,
			).(*ExternalServiceTarget)
			require.True(t, ok)

			resultIsNil, err := tt.invoke(t.Context(), target)
			require.Error(t, err)
			require.True(t, resultIsNil)

			responseError, ok := errors.AsType[*ExternalServiceTargetResponseError](err)
			require.True(t, ok)
			require.Equal(t, tt.operation, responseError.Operation)
			require.Equal(t, tt.detail, responseError.Detail)

			metadata, ok := errors.AsType[extensions.InvocationMetadataProvider](err)
			require.True(t, ok)
			require.Equal(t, "test.extension", metadata.InvocationExtensionId())
			require.Equal(t, "1.2.3", metadata.InvocationExtensionVersion())
			require.Equal(t, tt.event, metadata.InvocationEvent())
		})
	}
}

// ---------- IgnoreFile method coverage for different targets ----------
func Test_ServiceTargetKind_IgnoreFile_Extended(t *testing.T) {
	assert.Equal(t, ".webappignore", AppServiceTarget.IgnoreFile())
	assert.Equal(t, ".funcignore", AzureFunctionTarget.IgnoreFile())
	assert.Equal(t, "", ContainerAppTarget.IgnoreFile())
	assert.Equal(t, "", StaticWebAppTarget.IgnoreFile())
	assert.Equal(t, "", AksTarget.IgnoreFile())
}

// ---------- SupportsDelayedProvisioning ----------
func Test_ServiceTargetKind_SupportsDelayedProvisioning_Extended(t *testing.T) {
	assert.True(t, AksTarget.SupportsDelayedProvisioning())
	assert.False(t, AppServiceTarget.SupportsDelayedProvisioning())
	assert.False(t, ContainerAppTarget.SupportsDelayedProvisioning())
}
