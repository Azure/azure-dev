// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type readyClientStub struct {
	err error
}

func (s *readyClientStub) Ready(ctx context.Context, in *ReadyRequest, opts ...grpc.CallOption) (*ReadyResponse, error) {
	return &ReadyResponse{}, s.err
}

func TestCallReady(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		client  *AzdClient
		wantErr bool
		message string
	}{
		{
			name:    "success",
			client:  &AzdClient{extensionClient: &readyClientStub{}},
			wantErr: false,
		},
		{
			name:    "canceled",
			client:  &AzdClient{extensionClient: &readyClientStub{err: status.Error(codes.Canceled, "ctx cancelled")}},
			wantErr: false,
		},
		{
			name:    "unavailable",
			client:  &AzdClient{extensionClient: &readyClientStub{err: status.Error(codes.Unavailable, "shutdown")}},
			wantErr: false,
		},
		{
			name:    "other error",
			client:  &AzdClient{extensionClient: &readyClientStub{err: status.Error(codes.Internal, "boom")}},
			wantErr: true,
			message: "status=Internal",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := callReady(context.Background(), tt.client)
			if tt.wantErr {
				require.Error(t, err)
				if tt.message != "" {
					require.ErrorContains(t, err, tt.message)
				}
				return
			}

			require.NoError(t, err)
		})
	}
}

type fakeServiceTargetManager struct {
	registerCount atomic.Int32
	lastHost      atomic.Value
	err           error
	closed        atomic.Bool
}

func (f *fakeServiceTargetManager) Register(ctx context.Context, factory func() ServiceTargetProvider, hostType string) error {
	f.registerCount.Add(1)
	f.lastHost.Store(hostType)
	if f.err != nil {
		return f.err
	}
	return nil
}

func (f *fakeServiceTargetManager) Close() error {
	f.closed.Store(true)
	return nil
}

type fakeEventManager struct {
	projectHandlers map[string]ProjectEventHandler
	serviceHandlers map[string]ServiceEventHandler
	receiveErr      error
	closed          atomic.Bool
}

func newFakeEventManager() *fakeEventManager {
	return &fakeEventManager{
		projectHandlers: make(map[string]ProjectEventHandler),
		serviceHandlers: make(map[string]ServiceEventHandler),
	}
}

func (f *fakeEventManager) AddProjectEventHandler(ctx context.Context, eventName string, handler ProjectEventHandler) error {
	f.projectHandlers[eventName] = handler
	return nil
}

func (f *fakeEventManager) AddServiceEventHandler(
	ctx context.Context, eventName string, handler ServiceEventHandler, options *ServerEventOptions,
) error {
	f.serviceHandlers[eventName] = handler
	return nil
}

func (f *fakeEventManager) Receive(ctx context.Context) error {
	<-ctx.Done()
	if f.receiveErr != nil {
		return f.receiveErr
	}
	return nil
}

func (f *fakeEventManager) Close() error {
	f.closed.Store(true)
	return nil
}

type stubServiceTargetProvider struct{}

func (stubServiceTargetProvider) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	return nil
}
func (stubServiceTargetProvider) Endpoints(
	ctx context.Context, serviceConfig *ServiceConfig, targetResource *TargetResource,
) ([]string, error) {
	return nil, nil
}
func (stubServiceTargetProvider) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *ServiceConfig,
	defaultResolver func() (*TargetResource, error),
) (*TargetResource, error) {
	return nil, nil
}
func (stubServiceTargetProvider) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	progress ProgressReporter,
) (*ServicePackageResult, error) {
	return nil, nil
}
func (stubServiceTargetProvider) Publish(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *TargetResource,
	publishOptions *PublishOptions,
	progress ProgressReporter,
) (*ServicePublishResult, error) {
	return nil, nil
}
func (stubServiceTargetProvider) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	serviceContext *ServiceContext,
	targetResource *TargetResource,
	progress ProgressReporter,
) (*ServiceDeployResult, error) {
	return nil, nil
}

func TestExtensionHost_ServiceTargetOnly(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	runner := NewExtensionHost(client)

	fakeManager := &fakeServiceTargetManager{}
	runner.newServiceTargetManager = func(*AzdClient) serviceTargetRegistrar { return fakeManager }

	readyCalled := atomic.Bool{}
	runner.readyFn = func(ctx context.Context) error {
		readyCalled.Store(true)
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	runner.WithServiceTarget("demo", func() ServiceTargetProvider {
		return &stubServiceTargetProvider{}
	})

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	require.NoError(t, runner.Run(ctx))
	require.Equal(t, int32(1), fakeManager.registerCount.Load())
	require.True(t, readyCalled.Load())
	require.True(t, fakeManager.closed.Load())
}

func TestExtensionHost_EventHandlersOnly(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	runner := NewExtensionHost(client)

	fakeManager := newFakeEventManager()
	runner.newEventManager = func(*AzdClient) extensionEventManager { return fakeManager }
	runner.readyFn = func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	runner.WithProjectEventHandler("preprovision", func(ctx context.Context, args *ProjectEventArgs) error {
		return nil
	})
	runner.WithServiceEventHandler("prepackage", func(ctx context.Context, args *ServiceEventArgs) error {
		return nil
	}, nil)

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	require.NoError(t, runner.Run(ctx))
	require.Contains(t, fakeManager.projectHandlers, "preprovision")
	require.Contains(t, fakeManager.serviceHandlers, "prepackage")
	require.True(t, fakeManager.closed.Load())
}

func TestExtensionHost_ServiceTargetsAndEvents(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	runner := NewExtensionHost(client)

	serviceManager := &fakeServiceTargetManager{}
	eventManager := newFakeEventManager()

	runner.newServiceTargetManager = func(*AzdClient) serviceTargetRegistrar { return serviceManager }
	runner.newEventManager = func(*AzdClient) extensionEventManager { return eventManager }
	runner.readyFn = func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())

	runner.WithServiceTarget("foundryagent", func() ServiceTargetProvider {
		return &stubServiceTargetProvider{}
	})
	runner.WithProjectEventHandler("preprovision", func(ctx context.Context, args *ProjectEventArgs) error { return nil })

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	require.NoError(t, runner.Run(ctx))
	require.Equal(t, int32(1), serviceManager.registerCount.Load())
	require.Contains(t, eventManager.projectHandlers, "preprovision")
	require.True(t, serviceManager.closed.Load())
}

func TestExtensionHost_ServiceTargetRegistrationError(t *testing.T) {
	t.Parallel()

	client := &AzdClient{}
	runner := NewExtensionHost(client)

	serviceManager := &fakeServiceTargetManager{err: status.Error(codes.Internal, "boom")}
	runner.newServiceTargetManager = func(*AzdClient) serviceTargetRegistrar { return serviceManager }
	runner.readyFn = func(ctx context.Context) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner.WithServiceTarget("bad", func() ServiceTargetProvider {
		return &stubServiceTargetProvider{}
	})

	err := runner.Run(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to register service target")
	require.True(t, serviceManager.closed.Load())
}
