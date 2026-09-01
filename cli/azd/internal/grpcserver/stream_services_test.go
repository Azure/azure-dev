// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/lazy"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinput"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type registerStream[T any] struct {
	grpc.ServerStream
	ctx          context.Context
	request      *T
	received     bool
	response     *T
	responseSent chan struct{}
}

func newRegisterStream[T any](
	ctx context.Context,
	request *T,
) *registerStream[T] {
	return &registerStream[T]{
		ctx:          ctx,
		request:      request,
		responseSent: make(chan struct{}),
	}
}

func (s *registerStream[T]) Context() context.Context {
	return s.ctx
}

func (s *registerStream[T]) Recv() (*T, error) {
	if !s.received {
		s.received = true
		return s.request, nil
	}

	<-s.responseSent
	return nil, io.EOF
}

func (s *registerStream[T]) Send(response *T) error {
	s.response = response
	close(s.responseSent)
	return nil
}

type failingStream[T any] struct {
	grpc.ServerStream
	ctx context.Context
	err error
}

func newFailingStream[T any](
	ctx context.Context,
	err error,
) *failingStream[T] {
	return &failingStream[T]{ctx: ctx, err: err}
}

func (s *failingStream[T]) Context() context.Context {
	return s.ctx
}

func (s *failingStream[T]) Recv() (*T, error) {
	return nil, s.err
}

func (s *failingStream[T]) Send(*T) error {
	return nil
}

func newStreamTestExtensionManager(
	t *testing.T,
	extension *extensions.Extension,
) *extensions.Manager {
	t.Helper()

	mockCtx := mocks.NewMockContext(t.Context())
	userConfigManager := config.NewUserConfigManager(mockCtx.ConfigManager)
	sourceManager := extensions.NewSourceManager(
		mockCtx.Container,
		userConfigManager,
		mockCtx.HttpClient,
	)
	lazyRunner := lazy.NewLazy(func() (*extensions.Runner, error) {
		return extensions.NewRunner(mockCtx.CommandRunner), nil
	})
	manager, err := extensions.NewManager(
		userConfigManager,
		sourceManager,
		lazyRunner,
		mockCtx.HttpClient,
	)
	require.NoError(t, err)

	cfg, err := userConfigManager.Load()
	require.NoError(t, err)
	if extension != nil {
		require.NoError(t, cfg.Set(
			"extension.installed",
			map[string]*extensions.Extension{extension.Id: extension},
		))
	}

	return manager
}

func extensionClaimsContext(ctx context.Context, extensionID string) context.Context {
	return extensions.WithClaimsContext(ctx, &extensions.ExtensionClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: extensionID},
	})
}

func TestFrameworkService_Stream_RegistersProvider(t *testing.T) {
	extension := &extensions.Extension{
		Id:           "test.framework",
		Capabilities: []extensions.CapabilityType{extensions.FrameworkServiceProviderCapability},
	}
	manager := newStreamTestExtensionManager(t, extension)
	container := ioc.NewNestedContainer(nil)
	ioc.RegisterInstance[input.Console](container, mockinput.NewMockConsole())
	service := NewFrameworkService(container, manager, nil).(*FrameworkService)
	stream := newRegisterStream(
		extensionClaimsContext(t.Context(), extension.Id),
		&azdext.FrameworkServiceMessage{
			MessageType: &azdext.FrameworkServiceMessage_RegisterFrameworkServiceRequest{
				RegisterFrameworkServiceRequest: &azdext.RegisterFrameworkServiceRequest{
					Language: "rust",
				},
			},
		},
	)

	require.NoError(t, service.Stream(stream))
	require.IsType(
		t,
		&azdext.FrameworkServiceMessage_RegisterFrameworkServiceResponse{},
		stream.response.MessageType,
	)
	require.Empty(t, service.providerMap)
}

func TestProvisioningService_Stream_RegistersProvider(t *testing.T) {
	extension := &extensions.Extension{
		Id:           "test.provisioning",
		Capabilities: []extensions.CapabilityType{extensions.ProvisioningProviderCapability},
	}
	manager := newStreamTestExtensionManager(t, extension)
	service := NewProvisioningService(
		ioc.NewNestedContainer(nil),
		manager,
	).(*ProvisioningService)
	stream := newRegisterStream(
		extensionClaimsContext(t.Context(), extension.Id),
		&azdext.ProvisioningMessage{
			MessageType: &azdext.ProvisioningMessage_RegisterProvisioningProviderRequest{
				RegisterProvisioningProviderRequest: &azdext.RegisterProvisioningProviderRequest{
					Name: "test-provider",
				},
			},
		},
	)

	require.NoError(t, service.Stream(stream))
	require.IsType(
		t,
		&azdext.ProvisioningMessage_RegisterProvisioningProviderResponse{},
		stream.response.MessageType,
	)
	require.Empty(t, service.providerMap)
}

func TestServiceTargetService_Stream_RegistersProvider(t *testing.T) {
	extension := &extensions.Extension{
		Id:           "test.target",
		Capabilities: []extensions.CapabilityType{extensions.ServiceTargetProviderCapability},
	}
	manager := newStreamTestExtensionManager(t, extension)
	service := NewServiceTargetService(
		ioc.NewNestedContainer(nil),
		manager,
		nil,
	).(*ServiceTargetService)
	stream := newRegisterStream(
		extensionClaimsContext(t.Context(), extension.Id),
		&azdext.ServiceTargetMessage{
			MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetRequest{
				RegisterServiceTargetRequest: &azdext.RegisterServiceTargetRequest{
					Host: "test-target",
				},
			},
		},
	)

	require.NoError(t, service.Stream(stream))
	require.IsType(
		t,
		&azdext.ServiceTargetMessage_RegisterServiceTargetResponse{},
		stream.response.MessageType,
	)
	require.Empty(t, service.providerMap)
}

func TestEventService_EventStream_HandlesInvalidSubscription(t *testing.T) {
	extension := &extensions.Extension{
		Id:           "test.events",
		Capabilities: []extensions.CapabilityType{extensions.LifecycleEventsCapability},
	}
	manager := newStreamTestExtensionManager(t, extension)
	service, _ := createTestEventService()
	service.extensionManager = manager
	stream := newRegisterStream(
		extensionClaimsContext(t.Context(), extension.Id),
		&azdext.EventMessage{
			MessageType: &azdext.EventMessage_SubscribeProjectEvent{
				SubscribeProjectEvent: &azdext.SubscribeProjectEvent{},
			},
		},
	)

	require.NoError(t, service.EventStream(stream))
	require.NotNil(t, stream.response)
}

func TestExtensionService_Ready_InitializesExtension(t *testing.T) {
	extension := &extensions.Extension{Id: "test.ready"}
	service := NewExtensionService(
		newStreamTestExtensionManager(t, extension),
	).(*ExtensionService)

	response, err := service.Ready(
		extensionClaimsContext(t.Context(), extension.Id),
		&azdext.ReadyRequest{},
	)

	require.NoError(t, err)
	require.NotNil(t, response)
}

func TestExtensionService_ReportError_StoresStructuredError(t *testing.T) {
	extension := &extensions.Extension{Id: "test.error", Version: "1.2.3"}
	manager := newStreamTestExtensionManager(t, extension)
	service := NewExtensionService(
		manager,
	).(*ExtensionService)
	reported := &azdext.LocalError{Message: "extension failed"}

	response, err := service.ReportError(
		extensionClaimsContext(t.Context(), extension.Id),
		&azdext.ReportErrorRequest{Error: azdext.WrapError(reported)},
	)

	require.NoError(t, err)
	require.NotNil(t, response)

	storedExtension, err := manager.GetInstalled(extensions.FilterOptions{Id: extension.Id})
	require.NoError(t, err)
	metadata, ok := errors.AsType[extensions.InvocationMetadataProvider](storedExtension.GetReportedError())
	require.True(t, ok)
	require.Equal(t, storedExtension.Id, metadata.InvocationExtensionId())
	require.Equal(t, storedExtension.Version, metadata.InvocationExtensionVersion())
	require.Empty(t, metadata.InvocationEvent())
}

func TestProviderStreams_RejectUnsupportedCapabilities(t *testing.T) {
	extension := &extensions.Extension{Id: "test.unsupported"}
	manager := newStreamTestExtensionManager(t, extension)
	ctx := extensionClaimsContext(t.Context(), extension.Id)

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "Framework",
			run: func() error {
				return NewFrameworkService(
					ioc.NewNestedContainer(nil),
					manager,
					nil,
				).(*FrameworkService).Stream(newRegisterStream(
					ctx,
					&azdext.FrameworkServiceMessage{},
				))
			},
		},
		{
			name: "Provisioning",
			run: func() error {
				return NewProvisioningService(
					ioc.NewNestedContainer(nil),
					manager,
				).(*ProvisioningService).Stream(newRegisterStream(
					ctx,
					&azdext.ProvisioningMessage{},
				))
			},
		},
		{
			name: "ServiceTarget",
			run: func() error {
				return NewServiceTargetService(
					ioc.NewNestedContainer(nil),
					manager,
					nil,
				).(*ServiceTargetService).Stream(newRegisterStream(
					ctx,
					&azdext.ServiceTargetMessage{},
				))
			},
		},
		{
			name: "Validation",
			run: func() error {
				return NewValidationService(manager).Stream(newRegisterStream(
					ctx,
					&azdext.ValidationMessage{},
				))
			},
		},
		{
			name: "Events",
			run: func() error {
				service, _ := createTestEventService()
				service.extensionManager = manager
				return service.EventStream(newRegisterStream(
					ctx,
					&azdext.EventMessage{},
				))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, codes.PermissionDenied, status.Code(tt.run()))
		})
	}
}

func TestProviderStreams_RejectMissingExtensions(t *testing.T) {
	const extensionID = "test.missing"
	manager := newStreamTestExtensionManager(t, nil)
	ctx := extensionClaimsContext(t.Context(), extensionID)

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "Framework",
			run: func() error {
				return NewFrameworkService(
					ioc.NewNestedContainer(nil),
					manager,
					nil,
				).(*FrameworkService).Stream(newRegisterStream(
					ctx,
					&azdext.FrameworkServiceMessage{},
				))
			},
		},
		{
			name: "Provisioning",
			run: func() error {
				return NewProvisioningService(
					ioc.NewNestedContainer(nil),
					manager,
				).(*ProvisioningService).Stream(newRegisterStream(
					ctx,
					&azdext.ProvisioningMessage{},
				))
			},
		},
		{
			name: "ServiceTarget",
			run: func() error {
				return NewServiceTargetService(
					ioc.NewNestedContainer(nil),
					manager,
					nil,
				).(*ServiceTargetService).Stream(newRegisterStream(
					ctx,
					&azdext.ServiceTargetMessage{},
				))
			},
		},
		{
			name: "Validation",
			run: func() error {
				return NewValidationService(manager).Stream(newRegisterStream(
					ctx,
					&azdext.ValidationMessage{},
				))
			},
		},
		{
			name: "Events",
			run: func() error {
				service, _ := createTestEventService()
				service.extensionManager = manager
				return service.EventStream(newRegisterStream(
					ctx,
					&azdext.EventMessage{},
				))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, codes.FailedPrecondition, status.Code(tt.run()))
		})
	}
}

func TestProviderStreams_ReturnBrokerFailures(t *testing.T) {
	extension := &extensions.Extension{
		Id: "test.broker-failure",
		Capabilities: []extensions.CapabilityType{
			extensions.FrameworkServiceProviderCapability,
			extensions.ProvisioningProviderCapability,
			extensions.ServiceTargetProviderCapability,
			extensions.ValidationProviderCapability,
			extensions.LifecycleEventsCapability,
		},
	}
	manager := newStreamTestExtensionManager(t, extension)
	ctx := extensionClaimsContext(t.Context(), extension.Id)
	streamErr := errors.New("receive failed")

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "Framework",
			run: func() error {
				return NewFrameworkService(
					ioc.NewNestedContainer(nil),
					manager,
					nil,
				).(*FrameworkService).Stream(newFailingStream[azdext.FrameworkServiceMessage](
					ctx,
					streamErr,
				))
			},
		},
		{
			name: "Provisioning",
			run: func() error {
				return NewProvisioningService(
					ioc.NewNestedContainer(nil),
					manager,
				).(*ProvisioningService).Stream(newFailingStream[azdext.ProvisioningMessage](
					ctx,
					streamErr,
				))
			},
		},
		{
			name: "ServiceTarget",
			run: func() error {
				return NewServiceTargetService(
					ioc.NewNestedContainer(nil),
					manager,
					nil,
				).(*ServiceTargetService).Stream(newFailingStream[azdext.ServiceTargetMessage](
					ctx,
					streamErr,
				))
			},
		},
		{
			name: "Validation",
			run: func() error {
				return NewValidationService(manager).Stream(newFailingStream[azdext.ValidationMessage](
					ctx,
					streamErr,
				))
			},
		},
		{
			name: "Events",
			run: func() error {
				service, _ := createTestEventService()
				service.extensionManager = manager
				return service.EventStream(newFailingStream[azdext.EventMessage](
					ctx,
					streamErr,
				))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, tt.run(), "broker error")
		})
	}
}

func TestValidationService_Stream_RegistersCheck(t *testing.T) {
	extension := &extensions.Extension{
		Id:           "test.validation",
		Capabilities: []extensions.CapabilityType{extensions.ValidationProviderCapability},
	}
	manager := newStreamTestExtensionManager(t, extension)
	service := NewValidationService(manager)
	stream := newRegisterStream(
		extensionClaimsContext(t.Context(), extension.Id),
		&azdext.ValidationMessage{
			MessageType: &azdext.ValidationMessage_RegisterValidationCheckRequest{
				RegisterValidationCheckRequest: &azdext.RegisterValidationCheckRequest{
					CheckType: "provision",
					RuleId:    "test-rule",
				},
			},
		},
	)

	require.NoError(t, service.Stream(stream))
	require.IsType(
		t,
		&azdext.ValidationMessage_RegisterValidationCheckResponse{},
		stream.response.MessageType,
	)
	require.Empty(t, service.checks)
}
