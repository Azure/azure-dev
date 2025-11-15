// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/azure/azure-dev/pkg/grpcbroker"
	"github.com/google/uuid"
)

// ProgressReporter is an alias for the broker's ProgressFunc
type ProgressReporter = grpcbroker.ProgressFunc

var (
	ServiceTargetFactoryKey = func(config *ServiceConfig) string {
		return string(config.Host)
	}
)

// ServiceTargetProvider defines the interface for service target logic.
type ServiceTargetProvider interface {
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
	Endpoints(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		targetResource *TargetResource,
	) ([]string, error)
	GetTargetResource(
		ctx context.Context,
		subscriptionId string,
		serviceConfig *ServiceConfig,
		defaultResolver func() (*TargetResource, error),
	) (*TargetResource, error)
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress ProgressReporter,
	) (*ServicePackageResult, error)
	Publish(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *TargetResource,
		publishOptions *PublishOptions,
		progress ProgressReporter,
	) (*ServicePublishResult, error)
	Deploy(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		targetResource *TargetResource,
		progress ProgressReporter,
	) (*ServiceDeployResult, error)
}

// ServiceTargetManager handles registration and provisioning request forwarding for a provider.
type ServiceTargetManager struct {
	extensionId      string
	client           *AzdClient
	broker           *grpcbroker.MessageBroker[ServiceTargetMessage]
	componentManager *ComponentManager[ServiceTargetProvider]

	// Synchronization for concurrent access
	mu sync.RWMutex
}

// NewServiceTargetManager creates a new ServiceTargetManager for an AzdClient.
func NewServiceTargetManager(extensionId string, client *AzdClient) *ServiceTargetManager {
	return &ServiceTargetManager{
		extensionId:      extensionId,
		client:           client,
		componentManager: NewComponentManager[ServiceTargetProvider](ServiceTargetFactoryKey, "service target"),
	}
}

// Close terminates the underlying gRPC stream if it's been initialized.
// This method is thread-safe for concurrent access.
func (m *ServiceTargetManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.broker != nil {
		m.broker.Close()
		m.broker = nil
	}

	return m.componentManager.Close()
}

// ensureStream initializes the broker and stream if they haven't been created yet.
// This method is thread-safe for concurrent access.
func (m *ServiceTargetManager) ensureStream(ctx context.Context) error {
	// Fast path with read lock
	m.mu.RLock()
	if m.broker != nil {
		m.mu.RUnlock()
		return nil
	}
	m.mu.RUnlock()

	// Slow path with write lock
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if m.broker != nil {
		return nil
	}

	stream, err := m.client.ServiceTarget().Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create service target stream: %w", err)
	}

	// Create broker with client stream
	envelope := &ServiceTargetEnvelope{}
	// Use client as name since we're on the client side (extension process)
	m.broker = grpcbroker.NewMessageBroker(stream, envelope, m.extensionId)

	// Register handlers for incoming requests
	if err := m.broker.On(m.onInitialize); err != nil {
		return fmt.Errorf("failed to register initialize handler: %w", err)
	}
	if err := m.broker.On(m.onGetTargetResource); err != nil {
		return fmt.Errorf("failed to register get target resource handler: %w", err)
	}
	if err := m.broker.On(m.onPackage); err != nil {
		return fmt.Errorf("failed to register package handler: %w", err)
	}
	if err := m.broker.On(m.onPublish); err != nil {
		return fmt.Errorf("failed to register publish handler: %w", err)
	}
	if err := m.broker.On(m.onDeploy); err != nil {
		return fmt.Errorf("failed to register deploy handler: %w", err)
	}
	if err := m.broker.On(m.onEndpoints); err != nil {
		return fmt.Errorf("failed to register endpoints handler: %w", err)
	}

	return nil
}

// Register registers the provider with the server, waits for the response,
// then starts background handling of provisioning requests.
func (m *ServiceTargetManager) Register(ctx context.Context, factory ServiceTargetFactory, hostType string) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	m.componentManager.RegisterFactory(hostType, factory)

	registerReq := &ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &ServiceTargetMessage_RegisterServiceTargetRequest{
			RegisterServiceTargetRequest: &RegisterServiceTargetRequest{
				Host: hostType,
			},
		},
	}

	resp, err := m.broker.SendAndWait(ctx, registerReq)
	if err != nil {
		return fmt.Errorf("service target registration failed: %w", err)
	}

	if resp.GetRegisterServiceTargetResponse() == nil {
		return fmt.Errorf("expected RegisterServiceTargetResponse, got %T", resp.GetMessageType())
	}

	return nil
}

// Receive starts the broker's message dispatcher and blocks until the stream completes.
// This method ensures the stream is initialized then runs the broker.
func (m *ServiceTargetManager) Receive(ctx context.Context) error {
	// Ensure stream is initialized (this handles all locking internally)
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	// Run the broker (this blocks until context is canceled or error)
	return m.broker.Run(ctx)
}

// Ready blocks until the message broker starts receiving messages or the context is cancelled.
// This ensures the stream is initialized and then waits for the broker to be ready.
// Returns nil when ready, or context error if the context is cancelled before ready.
func (m *ServiceTargetManager) Ready(ctx context.Context) error {
	// Ensure stream is initialized (this handles all locking internally)
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	// Now that broker is guaranteed to exist, wait for it to be ready
	return m.broker.Ready(ctx)
}

// Handler methods - these are registered with the broker to handle incoming requests

// onInitialize handles initialization requests from the server
func (m *ServiceTargetManager) onInitialize(
	ctx context.Context,
	req *ServiceTargetInitializeRequest,
) (*ServiceTargetMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for initialize request")
	}

	// Create new instance using componentManager
	_, err := m.componentManager.GetOrCreateInstance(ctx, req.ServiceConfig)

	return &ServiceTargetMessage{
		MessageType: &ServiceTargetMessage_InitializeResponse{
			InitializeResponse: &ServiceTargetInitializeResponse{},
		},
	}, err
}

// onGetTargetResource handles get target resource requests
func (m *ServiceTargetManager) onGetTargetResource(
	ctx context.Context,
	req *GetTargetResourceRequest,
) (*ServiceTargetMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for get target resource request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	// Create a callback that returns the default target resource or error
	defaultResolver := func() (*TargetResource, error) {
		// Check if default resolution had an error
		if req.DefaultError != "" {
			return nil, errors.New(req.DefaultError)
		}
		// Return the default target resource (may be nil if not computed)
		return req.DefaultTargetResource, nil
	}

	result, err := provider.GetTargetResource(
		ctx,
		req.SubscriptionId,
		req.ServiceConfig,
		defaultResolver,
	)

	return &ServiceTargetMessage{
		MessageType: &ServiceTargetMessage_GetTargetResourceResponse{
			GetTargetResourceResponse: &GetTargetResourceResponse{TargetResource: result},
		},
	}, err
}

// onPackage handles package requests with progress reporting
func (m *ServiceTargetManager) onPackage(
	ctx context.Context,
	req *ServiceTargetPackageRequest,
	progress grpcbroker.ProgressFunc,
) (*ServiceTargetMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for package request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	result, err := provider.Package(
		ctx,
		req.ServiceConfig,
		req.ServiceContext,
		progress,
	)

	return &ServiceTargetMessage{
		MessageType: &ServiceTargetMessage_PackageResponse{
			PackageResponse: &ServiceTargetPackageResponse{Result: result},
		},
	}, err
}

// onPublish handles publish requests with progress reporting
func (m *ServiceTargetManager) onPublish(
	ctx context.Context,
	req *ServiceTargetPublishRequest,
	progress grpcbroker.ProgressFunc,
) (*ServiceTargetMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for publish request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	result, err := provider.Publish(
		ctx,
		req.ServiceConfig,
		req.ServiceContext,
		req.TargetResource,
		req.PublishOptions,
		progress,
	)

	return &ServiceTargetMessage{
		MessageType: &ServiceTargetMessage_PublishResponse{
			PublishResponse: &ServiceTargetPublishResponse{Result: result},
		},
	}, err
}

// onDeploy handles deploy requests with progress reporting
func (m *ServiceTargetManager) onDeploy(
	ctx context.Context,
	req *ServiceTargetDeployRequest,
	progress grpcbroker.ProgressFunc,
) (*ServiceTargetMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for deploy request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	result, err := provider.Deploy(
		ctx,
		req.ServiceConfig,
		req.ServiceContext,
		req.TargetResource,
		progress,
	)

	return &ServiceTargetMessage{
		MessageType: &ServiceTargetMessage_DeployResponse{
			DeployResponse: &ServiceTargetDeployResponse{Result: result},
		},
	}, err
}

// onEndpoints handles endpoints requests
func (m *ServiceTargetManager) onEndpoints(
	ctx context.Context,
	req *ServiceTargetEndpointsRequest,
) (*ServiceTargetMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for endpoints request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	endpoints, err := provider.Endpoints(
		ctx,
		req.ServiceConfig,
		req.TargetResource,
	)

	return &ServiceTargetMessage{
		MessageType: &ServiceTargetMessage_EndpointsResponse{
			EndpointsResponse: &ServiceTargetEndpointsResponse{Endpoints: endpoints},
		},
	}, err
}
