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
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
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
	target := NewExternalServiceTarget("test-target", ContainerAppTarget, nil, nil, nil, nil, nil, nil)
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

func Test_ExternalServiceTarget_Preview(t *testing.T) {
	serviceConfig := &ServiceConfig{Name: "agent", Host: "azure.ai.agent", RelativePath: "src/agent"}

	t.Run("not supported without preview registration", func(t *testing.T) {
		target := NewExternalServiceTarget("agent", "azure.ai.agent", nil, nil, nil, nil, nil, nil)
		_, err := target.(ServiceTargetPreviewer).Preview(t.Context(), serviceConfig)
		require.ErrorIs(t, err, ErrDeployPreviewNotSupported)
	})

	t.Run("forwards service config", func(t *testing.T) {
		want := &ServiceDeployPreviewResult{Message: "1 change"}
		target := NewExternalServiceTarget("agent", "azure.ai.agent", nil, nil, nil, nil, nil,
			func(ctx context.Context, protoConfig *azdext.ServiceConfig) (*ServiceDeployPreviewResult, error) {
				assert.Equal(t, "agent", protoConfig.Name)
				assert.Equal(t, "azure.ai.agent", protoConfig.Host)
				return want, nil
			})

		result, err := target.(ServiceTargetPreviewer).Preview(t.Context(), serviceConfig)
		require.NoError(t, err)
		assert.Same(t, want, result)
	})

	t.Run("snapshot expansion preserves source and caller state", func(t *testing.T) {
		service := &ServiceConfig{
			Name: "agent", Host: "azure.ai.agent",
			Environment:          osutil.ExpandableMap{"MODEL": osutil.NewExpandableString("${DEPLOYMENT}")},
			AdditionalProperties: map[string]any{"$ref": "agent.yaml", "kind": "hosted"},
		}
		env := environment.NewWithValues("preview", map[string]string{"DEPLOYMENT": "model"})
		initialValues := env.Dotenv()
		called := false
		target := NewExternalServiceTarget("agent", "azure.ai.agent", nil, nil, nil, nil, lazy.From(env),
			func(ctx context.Context, config *azdext.ServiceConfig) (*ServiceDeployPreviewResult, error) {
				called = true
				require.Equal(t, map[string]string{"MODEL": "model"}, config.Environment)
				require.Equal(t, "agent.yaml", config.AdditionalProperties.AsMap()["$ref"])
				return &ServiceDeployPreviewResult{}, nil
			})
		_, err := target.(ServiceTargetPreviewer).Preview(t.Context(), service)
		require.NoError(t, err)
		require.True(t, called)
		require.Equal(t, "${DEPLOYMENT}", service.Environment["MODEL"].Raw())
		require.Equal(t, "agent.yaml", service.AdditionalProperties["$ref"])
		require.Equal(t, initialValues, env.Dotenv())
	})

	for _, tc := range []struct {
		name        string
		service     *ServiceConfig
		envErr      error
		callbackErr error
		called      bool
	}{
		{name: "nil config"},
		{name: "environment failure", service: serviceConfig, envErr: errors.New("snapshot unavailable")},
		{name: "invalid expansion", service: &ServiceConfig{
			Name: "agent", Image: osutil.NewExpandableString("${INVALID"),
		}},
		{name: "callback failure", service: serviceConfig, callbackErr: errors.New("provider read failed"), called: true},
		{name: "unsupported callback", service: serviceConfig, callbackErr: ErrDeployPreviewNotSupported, called: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			env := lazy.NewLazy(func() (*environment.Environment, error) { return nil, tc.envErr })
			target := NewExternalServiceTarget("agent", "azure.ai.agent",
				&extensions.Extension{Id: "test.extension", Version: "1.2.3"}, nil, nil, nil, env,
				func(context.Context, *azdext.ServiceConfig) (*ServiceDeployPreviewResult, error) {
					called = true
					return nil, tc.callbackErr
				})
			result, err := target.(ServiceTargetPreviewer).Preview(t.Context(), tc.service)
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, tc.called, called)
			if tc.envErr != nil {
				require.ErrorIs(t, err, tc.envErr)
			}
			if tc.callbackErr != nil {
				require.ErrorIs(t, err, tc.callbackErr)
				metadata, ok := errors.AsType[extensions.InvocationMetadataProvider](err)
				require.True(t, ok)
				require.Equal(t, "service_target.preview", metadata.InvocationEvent())
			}
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
