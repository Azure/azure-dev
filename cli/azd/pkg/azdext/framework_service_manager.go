// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext/grpcbroker"
	"github.com/google/uuid"
)

var (
	FrameworkServiceFactoryKey = func(config *ServiceConfig) string {
		return string(config.Language)
	}
)


// FrameworkServiceManager handles registration and request forwarding for a framework service provider.
type FrameworkServiceManager struct {
	extensionId      string
	client           *AzdClient
	broker           *grpcbroker.MessageBroker[FrameworkServiceMessage]
	componentManager *ComponentManager[FrameworkServiceProvider]

	// Synchronization for concurrent access
	mu sync.RWMutex
}

// NewFrameworkServiceManager creates a new FrameworkServiceManager for an AzdClient.
func NewFrameworkServiceManager(extensionId string, client *AzdClient) *FrameworkServiceManager {
	return &FrameworkServiceManager{
		extensionId:      extensionId,
		client:           client,
		componentManager: NewComponentManager[FrameworkServiceProvider](FrameworkServiceFactoryKey, "framework service"),
	}
}

// Close closes the framework service manager and cleans up resources.
// This method is thread-safe for concurrent access.
func (m *FrameworkServiceManager) Close() error {
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
func (m *FrameworkServiceManager) ensureStream(ctx context.Context) error {
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
	stream, err := m.client.FrameworkService().Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create framework service stream: %w", err)
	}

	// Create broker with client stream
	envelope := &FrameworkServiceEnvelope{}
	// Use client as name since we're on the client side (extension process)
	m.broker = grpcbroker.NewMessageBroker(stream, envelope, m.extensionId)

	// Register handlers for incoming requests
	if err := m.broker.On(m.onInitialize); err != nil {
		return fmt.Errorf("failed to register initialize handler: %w", err)
	}
	if err := m.broker.On(m.onRequiredExternalTools); err != nil {
		return fmt.Errorf("failed to register required external tools handler: %w", err)
	}
	if err := m.broker.On(m.onRequirements); err != nil {
		return fmt.Errorf("failed to register requirements handler: %w", err)
	}
	if err := m.broker.On(m.onRestore); err != nil {
		return fmt.Errorf("failed to register restore handler: %w", err)
	}
	if err := m.broker.On(m.onBuild); err != nil {
		return fmt.Errorf("failed to register build handler: %w", err)
	}
	if err := m.broker.On(m.onPackage); err != nil {
		return fmt.Errorf("failed to register package handler: %w", err)
	}

	return nil
}

// Register registers a framework service provider with the specified language name.
func (m *FrameworkServiceManager) Register(
	ctx context.Context,
	factory FrameworkServiceFactory,
	language string,
) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	m.componentManager.RegisterFactory(language, factory)

	// Send registration request
	registerReq := &FrameworkServiceMessage{
		RequestId: uuid.NewString(),
		MessageType: &FrameworkServiceMessage_RegisterFrameworkServiceRequest{
			RegisterFrameworkServiceRequest: &RegisterFrameworkServiceRequest{
				Language: language,
			},
		},
	}

	resp, err := m.broker.SendAndWait(ctx, registerReq)
	if err != nil {
		return fmt.Errorf("framework service registration failed: %w", err)
	}

	if resp.GetRegisterFrameworkServiceResponse() == nil {
		return fmt.Errorf("expected RegisterFrameworkServiceResponse, got %T", resp.GetMessageType())
	}

	return nil
}

// Receive starts the broker's message dispatcher and blocks until the stream completes.
// This method ensures the stream is initialized then runs the broker.
func (m *FrameworkServiceManager) Receive(ctx context.Context) error {
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
func (m *FrameworkServiceManager) Ready(ctx context.Context) error {
	// Ensure stream is initialized (this handles all locking internally)
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	// Now that broker is guaranteed to exist, wait for it to be ready
	return m.broker.Ready(ctx)
}

// Handler methods - these are registered with the broker to handle incoming requests

// onInitialize handles initialization requests from the server
func (m *FrameworkServiceManager) onInitialize(
	ctx context.Context,
	req *FrameworkServiceInitializeRequest,
) (*FrameworkServiceMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for initialize request")
	}

	// Create new instance using componentManager
	_, err := m.componentManager.GetOrCreateInstance(ctx, req.ServiceConfig)

	return &FrameworkServiceMessage{
		MessageType: &FrameworkServiceMessage_InitializeResponse{
			InitializeResponse: &FrameworkServiceInitializeResponse{},
		},
	}, err
}

// onRequiredExternalTools handles required external tools requests
func (m *FrameworkServiceManager) onRequiredExternalTools(
	ctx context.Context,
	req *FrameworkServiceRequiredExternalToolsRequest,
) (*FrameworkServiceMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for required external tools request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	tools, err := provider.RequiredExternalTools(ctx, req.ServiceConfig)

	return &FrameworkServiceMessage{
		MessageType: &FrameworkServiceMessage_RequiredExternalToolsResponse{
			RequiredExternalToolsResponse: &FrameworkServiceRequiredExternalToolsResponse{
				Tools: tools,
			},
		},
	}, err
}

// onRequirements handles requirements requests
func (m *FrameworkServiceManager) onRequirements(
	ctx context.Context,
	req *FrameworkServiceRequirementsRequest,
) (*FrameworkServiceMessage, error) {
	// Requirements don't depend on a specific service, so we can use any available instance
	provider, err := m.componentManager.GetAnyInstance()
	if err != nil {
		return nil, errors.New("no provider instances available. Initialize must be called first")
	}

	requirements, err := provider.Requirements()

	return &FrameworkServiceMessage{
		MessageType: &FrameworkServiceMessage_RequirementsResponse{
			RequirementsResponse: &FrameworkServiceRequirementsResponse{
				Requirements: requirements,
			},
		},
	}, err
}

// onRestore handles restore requests with progress reporting
func (m *FrameworkServiceManager) onRestore(
	ctx context.Context,
	req *FrameworkServiceRestoreRequest,
	progress ProgressFunc,
) (*FrameworkServiceMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for restore request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	result, err := provider.Restore(ctx, req.ServiceConfig, req.ServiceContext, progress)

	return &FrameworkServiceMessage{
		MessageType: &FrameworkServiceMessage_RestoreResponse{
			RestoreResponse: &FrameworkServiceRestoreResponse{
				RestoreResult: result,
			},
		},
	}, err
}

// onBuild handles build requests with progress reporting
func (m *FrameworkServiceManager) onBuild(
	ctx context.Context,
	req *FrameworkServiceBuildRequest,
	progress ProgressFunc,
) (*FrameworkServiceMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for build request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	result, err := provider.Build(ctx, req.ServiceConfig, req.ServiceContext, progress)

	return &FrameworkServiceMessage{
		MessageType: &FrameworkServiceMessage_BuildResponse{
			BuildResponse: &FrameworkServiceBuildResponse{
				Result: result,
			},
		},
	}, err
}

// onPackage handles package requests with progress reporting
func (m *FrameworkServiceManager) onPackage(
	ctx context.Context,
	req *FrameworkServicePackageRequest,
	progress ProgressFunc,
) (*FrameworkServiceMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for package request")
	}

	provider, err := m.componentManager.GetInstance(req.ServiceConfig.Name)
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for service: %s. Initialize must be called first",
			req.ServiceConfig.Name)
	}

	result, err := provider.Package(ctx, req.ServiceConfig, req.ServiceContext, progress)

	return &FrameworkServiceMessage{
		MessageType: &FrameworkServiceMessage_PackageResponse{
			PackageResponse: &FrameworkServicePackageResponse{
				PackageResult: result,
			},
		},
	}, err
}
