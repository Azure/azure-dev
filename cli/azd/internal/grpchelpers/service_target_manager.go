// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpchelpers

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/grpcbroker"
	"github.com/google/uuid"
)

var (
	ServiceTargetFactoryKey = func(config *azdext.ServiceConfig) string {
		return string(config.Host)
	}
)

// ServiceTargetManager handles registration and provisioning request forwarding for a provider.
type ServiceTargetManager struct {
	extensionId      string
	client           *azdext.AzdClient
	broker           *grpcbroker.MessageBroker[azdext.ServiceTargetMessage]
	componentManager *azdext.ComponentManager[azdext.ServiceTargetProvider]

	// Synchronization for concurrent access
	mu sync.RWMutex
}

// toAzdextProgress converts grpcbroker.ProgressFunc to azdext.ProgressFunc
func toAzdextProgress(p grpcbroker.ProgressFunc) azdext.ProgressFunc {
	if p == nil {
		return nil
	}
	return azdext.ProgressFunc(p)
}

// NewServiceTargetManager creates a new ServiceTargetManager for an azdext.AzdClient.
func NewServiceTargetManager(extensionId string, client *azdext.AzdClient) *ServiceTargetManager {
	return &ServiceTargetManager{
		extensionId: extensionId,
		client:      client,
		componentManager: azdext.NewComponentManager[azdext.ServiceTargetProvider](
			ServiceTargetFactoryKey,
			"service target",
		),
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
	envelope := &azdext.ServiceTargetEnvelope{}
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

	registerReq := &azdext.ServiceTargetMessage{
		RequestId: uuid.NewString(),
		MessageType: &azdext.ServiceTargetMessage_RegisterServiceTargetRequest{
			RegisterServiceTargetRequest: &azdext.RegisterServiceTargetRequest{
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
	req *azdext.ServiceTargetInitializeRequest,
) (*azdext.ServiceTargetMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for initialize request")
	}

	// Create new instance using componentManager
	_, err := m.componentManager.GetOrCreateInstance(ctx, req.ServiceConfig)

	return &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_InitializeResponse{
			InitializeResponse: &azdext.ServiceTargetInitializeResponse{},
		},
	}, err
}

// onGetTargetResource handles get target resource requests
func (m *ServiceTargetManager) onGetTargetResource(
	ctx context.Context,
	req *azdext.GetTargetResourceRequest,
) (*azdext.ServiceTargetMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for get target resource request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	// Create a callback that returns the default target resource or error
	defaultResolver := func() (*azdext.TargetResource, error) {
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

	return &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_GetTargetResourceResponse{
			GetTargetResourceResponse: &azdext.GetTargetResourceResponse{TargetResource: result},
		},
	}, err
}

// onPackage handles package requests with progress reporting
func (m *ServiceTargetManager) onPackage(
	ctx context.Context,
	req *azdext.ServiceTargetPackageRequest,
	progress grpcbroker.ProgressFunc,
) (*azdext.ServiceTargetMessage, error) {
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
		toAzdextProgress(progress),
	)

	return &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_PackageResponse{
			PackageResponse: &azdext.ServiceTargetPackageResponse{Result: result},
		},
	}, err
}

// onPublish handles publish requests with progress reporting
func (m *ServiceTargetManager) onPublish(
	ctx context.Context,
	req *azdext.ServiceTargetPublishRequest,
	progress grpcbroker.ProgressFunc,
) (*azdext.ServiceTargetMessage, error) {
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
		toAzdextProgress(progress),
	)

	return &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_PublishResponse{
			PublishResponse: &azdext.ServiceTargetPublishResponse{Result: result},
		},
	}, err
}

// onDeploy handles deploy requests with progress reporting
func (m *ServiceTargetManager) onDeploy(
	ctx context.Context,
	req *azdext.ServiceTargetDeployRequest,
	progress grpcbroker.ProgressFunc,
) (*azdext.ServiceTargetMessage, error) {
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
		toAzdextProgress(progress),
	)

	return &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_DeployResponse{
			DeployResponse: &azdext.ServiceTargetDeployResponse{Result: result},
		},
	}, err
}

// onEndpoints handles endpoints requests
func (m *ServiceTargetManager) onEndpoints(
	ctx context.Context,
	req *azdext.ServiceTargetEndpointsRequest,
) (*azdext.ServiceTargetMessage, error) {
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

	return &azdext.ServiceTargetMessage{
		MessageType: &azdext.ServiceTargetMessage_EndpointsResponse{
			EndpointsResponse: &azdext.ServiceTargetEndpointsResponse{Endpoints: endpoints},
		},
	}, err
}
