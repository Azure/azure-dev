// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockHostReceiver struct {
	mock.Mock
}

func (m *mockHostReceiver) Receive(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockHostReceiver) Ready(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockHostReceiver) Close() error {
	return m.Called().Error(0)
}

type mockHostGenerationRegistrar struct {
	mockHostReceiver
}

func (m *mockHostGenerationRegistrar) Register(
	ctx context.Context, name string, provider InfraGenerationProvider,
) error {
	return m.Called(ctx, name, provider).Error(0)
}

type mockHostProvisioningRegistrar struct {
	mockHostReceiver
}

func (m *mockHostProvisioningRegistrar) Register(
	ctx context.Context, factory ProvisioningProviderFactory, name string,
) error {
	return m.Called(ctx, factory, name).Error(0)
}

type hostTestGenerator struct{}

func (*hostTestGenerator) Generate(
	context.Context, *v1beta.InfraGenerateRequest,
) (*v1beta.InfraGenerateResponse, error) {
	return nil, errors.New("registration must not invoke generation")
}

// expectHostReceiver models the broker's readiness barrier and joins its receive loop during cleanup.
func expectHostReceiver(t *testing.T, manager *mock.Mock, readyErr error) <-chan struct{} {
	t.Helper()
	started := make(chan struct{})
	stopped := make(chan struct{})
	closed := make(chan struct{})
	ready := make(chan struct{})
	manager.On("Receive", mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		close(started)
		defer close(stopped)
		select {
		case <-ctx.Done():
		case <-closed:
		}
	}).Return(nil).Once()
	manager.On("Ready", mock.Anything).Run(func(args mock.Arguments) {
		select {
		case <-started:
		case <-args.Get(0).(context.Context).Done():
		}
		close(ready)
	}).Return(readyErr).Once()
	manager.On("Close").Run(func(mock.Arguments) {
		close(closed)
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("receiver did not stop during cleanup")
		}
	}).Return(nil).Once()
	t.Cleanup(func() { manager.AssertExpectations(t) })
	return ready
}

func TestExtensionHost_GeneratorAndProvisioningLifecycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	generation := &mockHostGenerationRegistrar{}
	provisioning := &mockHostProvisioningRegistrar{}
	generationReady := expectHostReceiver(t, &generation.Mock, nil)
	provisioningReady := expectHostReceiver(t, &provisioning.Mock, nil)
	generator := &hostTestGenerator{}
	var registered atomic.Int32
	checkRegistration := func(mock.Arguments) {
		for _, ready := range []<-chan struct{}{generationReady, provisioningReady} {
			select {
			case <-ready:
			default:
				t.Error("every receiver must be ready before any registration")
			}
		}
		registered.Add(1)
	}
	generation.On("Register", mock.Anything, "first", generator).Run(checkRegistration).Return(nil).Once()
	generation.On("Register", mock.Anything, "second", generator).Run(checkRegistration).Return(nil).Once()
	provisioning.On("Register", mock.Anything, mock.Anything, "resources").Run(checkRegistration).Return(nil).Once()
	extension := &MockExtensionServiceClient{}
	extension.On("Ready", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			require.EqualValues(t, 3, registered.Load(), "all registrations must finish before host readiness")
			cancel()
		}).Return(&ReadyResponse{}, nil).Once()
	host := NewExtensionHost(&AzdClient{extensionClient: extension})
	host.infraGenerationManager = generation
	host.provisioningManager = provisioning
	require.Same(t, host, host.WithInfraGenerationProvider("first", generator))
	host.WithInfraGenerationProvider("second", generator)
	host.WithProvisioningProvider("resources", func() ProvisioningProvider {
		t.Error("host registration must not invoke a provisioning factory")
		return nil
	})
	registrationsSnapshot := host.InfraGenerationProviders()
	require.Len(t, registrationsSnapshot, 2)
	registrationsSnapshot[0].Name = "modified"
	require.Equal(t, "first", host.InfraGenerationProviders()[0].Name)

	require.NoError(t, host.Run(ctx))
	extension.AssertExpectations(t)
}

func TestExtensionHost_ValidationLifecycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := startFakeValidationServer(t, &fakeValidationServer{})
	extension := &MockExtensionServiceClient{}
	client.extensionClient = extension
	host := NewExtensionHost(client)
	require.Same(t, host, host.WithValidationCheck(ValidationCheckRegistration{
		CheckType: "provision",
		RuleID:    "test-rule",
		Factory:   func() ValidationCheckProvider { return &mockProvider{} },
	}))
	extension.On("Ready", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			host.validationManager.mu.RLock()
			_, registered := host.validationManager.factories[validationCheckKey{
				CheckType: "provision", RuleID: "test-rule",
			}]
			host.validationManager.mu.RUnlock()
			require.True(t, registered, "validation must be registered before host readiness")
			cancel()
		}).Return(&ReadyResponse{}, nil).Once()

	require.NoError(t, host.Run(ctx))
	host.validationManager.mu.RLock()
	defer host.validationManager.mu.RUnlock()
	require.Nil(t, host.validationManager.broker, "host shutdown must close the validation stream")
	require.Empty(t, host.validationManager.factories)
	extension.AssertExpectations(t)
}

func TestExtensionHost_ReceiverReadinessErrorsPreventRegistration(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	generation := &mockHostGenerationRegistrar{}
	provisioning := &mockHostProvisioningRegistrar{}
	generationErr := errors.New("generator receiver unavailable")
	provisioningErr := errors.New("provisioning receiver unavailable")
	expectHostReceiver(t, &generation.Mock, generationErr)
	expectHostReceiver(t, &provisioning.Mock, provisioningErr)
	extension := &MockExtensionServiceClient{}
	host := NewExtensionHost(&AzdClient{extensionClient: extension})
	host.infraGenerationManager = generation
	host.provisioningManager = provisioning
	host.WithInfraGenerationProvider("resources", &hostTestGenerator{})
	host.WithProvisioningProvider("resources", func() ProvisioningProvider { return nil })

	err := host.Run(ctx)
	require.ErrorIs(t, err, generationErr)
	require.ErrorIs(t, err, provisioningErr)
	generation.AssertNotCalled(t, "Register", mock.Anything, mock.Anything, mock.Anything)
	provisioning.AssertNotCalled(t, "Register", mock.Anything, mock.Anything, mock.Anything)
	extension.AssertNotCalled(t, "Ready", mock.Anything, mock.Anything, mock.Anything)
}

func TestExtensionHost_ProviderRegistrationErrorsCloseAllReceivers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	generation := &mockHostGenerationRegistrar{}
	provisioning := &mockHostProvisioningRegistrar{}
	expectHostReceiver(t, &generation.Mock, nil)
	expectHostReceiver(t, &provisioning.Mock, nil)
	generationErr := errors.New("generator registration rejected")
	provisioningErr := errors.New("provisioning registration rejected")
	generation.On("Register", mock.Anything, "resources", mock.Anything).Return(generationErr).Once()
	provisioning.On("Register", mock.Anything, mock.Anything, "resources").Return(provisioningErr).Once()
	extension := &MockExtensionServiceClient{}
	host := NewExtensionHost(&AzdClient{extensionClient: extension})
	host.infraGenerationManager = generation
	host.provisioningManager = provisioning
	host.WithInfraGenerationProvider("resources", &hostTestGenerator{})
	host.WithProvisioningProvider("resources", func() ProvisioningProvider { return nil })

	err := host.Run(ctx)
	require.ErrorIs(t, err, generationErr)
	require.ErrorIs(t, err, provisioningErr)
	extension.AssertNotCalled(t, "Ready", mock.Anything, mock.Anything, mock.Anything)
}

func TestExtensionHost_ValidationRegistrationFailureClosesStream(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := startFakeValidationServer(t, &fakeValidationServer{registerErr: errors.New("validation rejected")})
	extension := &MockExtensionServiceClient{}
	client.extensionClient = extension
	host := NewExtensionHost(client)
	host.WithValidationCheck(ValidationCheckRegistration{
		CheckType: "provision", RuleID: "test-rule",
		Factory: func() ValidationCheckProvider { return &mockProvider{} },
	})

	err := host.Run(ctx)
	require.ErrorContains(t, err, "validation rejected")
	host.validationManager.mu.RLock()
	defer host.validationManager.mu.RUnlock()
	require.Nil(t, host.validationManager.broker)
	require.Empty(t, host.validationManager.factories)
	extension.AssertNotCalled(t, "Ready", mock.Anything, mock.Anything, mock.Anything)
}

func TestExtensionHost_NilFactoriesCloseActiveReceivers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		configure func(*ExtensionHost) *mock.Mock
		wantError string
	}{
		{
			name: "service target",
			configure: func(host *ExtensionHost) *mock.Mock {
				manager := &MockServiceTargetRegistrar{}
				host.serviceTargetManager = manager
				host.WithServiceTarget("web", nil)
				return &manager.Mock
			},
			wantError: "service target provider for host 'web' is nil",
		},
		{
			name: "framework service",
			configure: func(host *ExtensionHost) *mock.Mock {
				manager := &MockFrameworkServiceRegistrar{}
				host.frameworkServiceManager = manager
				host.WithFrameworkService("python", nil)
				return &manager.Mock
			},
			wantError: "framework service provider for language 'python' is nil",
		},
		{
			name: "project event",
			configure: func(host *ExtensionHost) *mock.Mock {
				manager := &MockExtensionEventManager{}
				host.eventManager = manager
				host.WithProjectEventHandler("preprovision", nil)
				return &manager.Mock
			},
			wantError: "project event handler for 'preprovision' is nil",
		},
		{
			name: "service event",
			configure: func(host *ExtensionHost) *mock.Mock {
				manager := &MockExtensionEventManager{}
				host.eventManager = manager
				host.WithServiceEventHandler("predeploy", nil, nil)
				return &manager.Mock
			},
			wantError: "service event handler for 'predeploy' is nil",
		},
		{
			name: "provisioning provider",
			configure: func(host *ExtensionHost) *mock.Mock {
				manager := &mockHostProvisioningRegistrar{}
				host.provisioningManager = manager
				host.WithProvisioningProvider("resources", nil)
				return &manager.Mock
			},
			wantError: "provisioning provider for 'resources' is nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			extension := &MockExtensionServiceClient{}
			host := NewExtensionHost(&AzdClient{extensionClient: extension})
			expectHostReceiver(t, tt.configure(host), nil)

			require.EqualError(t, host.Run(ctx), tt.wantError)
			extension.AssertNotCalled(t, "Ready", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestExtensionHost_NilValidationFactoryClosesStream(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	client := startFakeValidationServer(t, &fakeValidationServer{})
	extension := &MockExtensionServiceClient{}
	client.extensionClient = extension
	host := NewExtensionHost(client)
	host.WithValidationCheck(ValidationCheckRegistration{CheckType: "provision", RuleID: "test-rule"})

	require.EqualError(t, host.Run(ctx), "validation check factory for 'provision/test-rule' is nil")
	host.validationManager.mu.RLock()
	defer host.validationManager.mu.RUnlock()
	require.Nil(t, host.validationManager.broker)
	extension.AssertNotCalled(t, "Ready", mock.Anything, mock.Anything, mock.Anything)
}

func TestExtensionHost_HostReadinessFailureClosesReceivers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	generation := &mockHostGenerationRegistrar{}
	expectHostReceiver(t, &generation.Mock, nil)
	generation.On("Register", mock.Anything, "resources", mock.Anything).Return(nil).Once()
	extension := &MockExtensionServiceClient{}
	readyErr := status.Error(codes.Internal, "readiness rejected")
	extension.On("Ready", mock.Anything, mock.Anything, mock.Anything).
		Return((*ReadyResponse)(nil), readyErr).Once()
	host := NewExtensionHost(&AzdClient{extensionClient: extension})
	host.infraGenerationManager = generation
	host.WithInfraGenerationProvider("resources", &hostTestGenerator{})

	require.ErrorIs(t, host.Run(ctx), readyErr)
	extension.AssertExpectations(t)
}

func TestExtensionHost_ReceiverFailureClosesOtherManagers(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	generation := &mockHostGenerationRegistrar{}
	expectHostReceiver(t, &generation.Mock, nil)
	generation.On("Register", mock.Anything, "resources", mock.Anything).Return(nil).Once()
	target := &MockServiceTargetRegistrar{}
	failReceiver := make(chan struct{})
	receiverErr := errors.New("service target stream failed")
	target.On("Receive", mock.Anything).Run(func(mock.Arguments) {
		<-failReceiver
	}).Return(receiverErr).Once()
	target.On("Ready", mock.Anything).Return(nil).Once()
	target.On("Register", mock.Anything, mock.Anything, "web").Return(nil).Once()
	target.On("Close").Return(nil).Once()
	extension := &MockExtensionServiceClient{}
	extension.On("Ready", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { close(failReceiver) }).Return(&ReadyResponse{}, nil).Once()
	host := NewExtensionHost(&AzdClient{extensionClient: extension})
	host.serviceTargetManager = target
	host.infraGenerationManager = generation
	host.WithServiceTarget("web", func() ServiceTargetProvider { return &MockServiceTargetProvider{} })
	host.WithInfraGenerationProvider("resources", &hostTestGenerator{})

	require.ErrorIs(t, host.Run(ctx), receiverErr)
	target.AssertExpectations(t)
	extension.AssertExpectations(t)
}

func TestExtensionHost_UnconfiguredManagersRemainInactive(t *testing.T) {
	t.Parallel()
	extension := &MockExtensionServiceClient{}
	extension.On("Ready", mock.Anything, mock.Anything, mock.Anything).Return(&ReadyResponse{}, nil).Once()
	generation := &mockHostGenerationRegistrar{}
	provisioning := &mockHostProvisioningRegistrar{}
	host := NewExtensionHost(&AzdClient{extensionClient: extension})
	host.infraGenerationManager = generation
	host.provisioningManager = provisioning

	require.NoError(t, host.Run(t.Context()))
	require.Empty(t, generation.Calls)
	require.Empty(t, provisioning.Calls)
	require.Nil(t, host.validationManager.broker)
	extension.AssertExpectations(t)
}
