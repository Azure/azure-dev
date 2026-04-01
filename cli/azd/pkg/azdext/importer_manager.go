// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

// ImporterFactory describes a function that creates an instance of an importer provider
type ImporterFactory = ProviderFactory[ImporterProvider]

// ImporterProvider defines the interface for importer logic.
type ImporterProvider interface {
	CanImport(ctx context.Context, svcConfig *ServiceConfig) (bool, error)
	Services(
		ctx context.Context,
		projectConfig *ProjectConfig,
		svcConfig *ServiceConfig,
	) (map[string]*ServiceConfig, error)
	ProjectInfrastructure(
		ctx context.Context,
		svcConfig *ServiceConfig,
		progress ProgressReporter,
	) (*ImporterProjectInfrastructureResponse, error)
	GenerateAllInfrastructure(
		ctx context.Context,
		projectConfig *ProjectConfig,
		svcConfig *ServiceConfig,
	) ([]*GeneratedFile, error)
}

// ImporterManager handles registration and request forwarding for an importer provider.
type ImporterManager struct {
	extensionId  string
	client       *AzdClient
	broker       *grpcbroker.MessageBroker[ImporterMessage]
	brokerLogger *log.Logger

	// Factory and cached instance for each registered importer
	factories map[string]ImporterFactory
	instances map[string]ImporterProvider

	// Synchronization for concurrent access
	mu sync.RWMutex
}

// NewImporterManager creates a new ImporterManager for an AzdClient.
func NewImporterManager(extensionId string, client *AzdClient, brokerLogger *log.Logger) *ImporterManager {
	return &ImporterManager{
		extensionId:  extensionId,
		client:       client,
		factories:    make(map[string]ImporterFactory),
		instances:    make(map[string]ImporterProvider),
		brokerLogger: brokerLogger,
	}
}

// Close closes the importer manager and cleans up resources.
// This method is thread-safe for concurrent access.
func (m *ImporterManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.broker != nil {
		m.broker.Close()
		m.broker = nil
	}

	return nil
}

// ensureStream initializes the broker and stream if they haven't been created yet.
// This method is thread-safe for concurrent access.
func (m *ImporterManager) ensureStream(ctx context.Context) error {
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

	stream, err := m.client.Importer().Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create importer stream: %w", err)
	}

	// Create broker with client stream
	envelope := &ImporterEnvelope{}
	// Use client as name since we're on the client side (extension process)
	m.broker = grpcbroker.NewMessageBroker(stream, envelope, m.extensionId, m.brokerLogger)

	// Register handlers for incoming requests
	if err := m.broker.On(m.onCanImport); err != nil {
		return fmt.Errorf("failed to register can import handler: %w", err)
	}
	if err := m.broker.On(m.onServices); err != nil {
		return fmt.Errorf("failed to register services handler: %w", err)
	}
	if err := m.broker.On(m.onProjectInfrastructure); err != nil {
		return fmt.Errorf("failed to register project infrastructure handler: %w", err)
	}
	if err := m.broker.On(m.onGenerateAllInfrastructure); err != nil {
		return fmt.Errorf("failed to register generate all infrastructure handler: %w", err)
	}

	return nil
}

// getAnyInstance returns any available provider instance, creating one if necessary.
// This is used by request handlers where the request doesn't carry the importer name,
// since the server routes requests to the correct extension's broker by importer name.
func (m *ImporterManager) getAnyInstance() (ImporterProvider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Return existing instance if available
	for _, instance := range m.instances {
		return instance, nil
	}

	// Create from first available factory
	for name, factory := range m.factories {
		instance := factory()
		m.instances[name] = instance
		return instance, nil
	}

	return nil, errors.New("no importer providers registered")
}

// Register registers an importer provider with the specified name.
func (m *ImporterManager) Register(
	ctx context.Context,
	factory ImporterFactory,
	name string,
) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	m.mu.Lock()
	m.factories[name] = factory
	m.mu.Unlock()

	// Send registration request
	registerReq := &ImporterMessage{
		RequestId: uuid.NewString(),
		MessageType: &ImporterMessage_RegisterImporterRequest{
			RegisterImporterRequest: &RegisterImporterRequest{
				Name: name,
			},
		},
	}

	resp, err := m.broker.SendAndWait(ctx, registerReq)
	if err != nil {
		return fmt.Errorf("importer registration failed: %w", err)
	}

	if resp.GetRegisterImporterResponse() == nil {
		return fmt.Errorf("expected RegisterImporterResponse, got %T", resp.GetMessageType())
	}

	return nil
}

// Receive starts the broker's message dispatcher and blocks until the stream completes.
// This method ensures the stream is initialized then runs the broker.
func (m *ImporterManager) Receive(ctx context.Context) error {
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
func (m *ImporterManager) Ready(ctx context.Context) error {
	// Ensure stream is initialized (this handles all locking internally)
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	// Now that broker is guaranteed to exist, wait for it to be ready
	return m.broker.Ready(ctx)
}

// Handler methods - these are registered with the broker to handle incoming requests

// onCanImport handles can import requests from the server
func (m *ImporterManager) onCanImport(
	ctx context.Context,
	req *ImporterCanImportRequest,
) (*ImporterMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for can import request")
	}

	provider, err := m.getAnyInstance()
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for importer: %w", err)
	}

	canImport, err := provider.CanImport(ctx, req.ServiceConfig)

	return &ImporterMessage{
		MessageType: &ImporterMessage_CanImportResponse{
			CanImportResponse: &ImporterCanImportResponse{
				CanImport: canImport,
			},
		},
	}, err
}

// onServices handles services requests from the server
func (m *ImporterManager) onServices(
	ctx context.Context,
	req *ImporterServicesRequest,
) (*ImporterMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for services request")
	}

	provider, err := m.getAnyInstance()
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for importer: %w", err)
	}

	services, err := provider.Services(ctx, req.ProjectConfig, req.ServiceConfig)

	return &ImporterMessage{
		MessageType: &ImporterMessage_ServicesResponse{
			ServicesResponse: &ImporterServicesResponse{
				Services: services,
			},
		},
	}, err
}

// onProjectInfrastructure handles project infrastructure requests with progress reporting
func (m *ImporterManager) onProjectInfrastructure(
	ctx context.Context,
	req *ImporterProjectInfrastructureRequest,
	progress grpcbroker.ProgressFunc,
) (*ImporterMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for project infrastructure request")
	}

	provider, err := m.getAnyInstance()
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for importer: %w", err)
	}

	result, err := provider.ProjectInfrastructure(ctx, req.ServiceConfig, progress)
	if err != nil {
		return nil, err
	}

	return &ImporterMessage{
		MessageType: &ImporterMessage_ProjectInfrastructureResponse{
			ProjectInfrastructureResponse: result,
		},
	}, nil
}

// onGenerateAllInfrastructure handles generate all infrastructure requests
func (m *ImporterManager) onGenerateAllInfrastructure(
	ctx context.Context,
	req *ImporterGenerateAllInfrastructureRequest,
) (*ImporterMessage, error) {
	if req.ServiceConfig == nil {
		return nil, errors.New("service config is required for generate all infrastructure request")
	}

	provider, err := m.getAnyInstance()
	if err != nil {
		return nil, fmt.Errorf("no provider instance found for importer: %w", err)
	}

	files, err := provider.GenerateAllInfrastructure(ctx, req.ProjectConfig, req.ServiceConfig)

	return &ImporterMessage{
		MessageType: &ImporterMessage_GenerateAllInfrastructureResponse{
			GenerateAllInfrastructureResponse: &ImporterGenerateAllInfrastructureResponse{
				Files: files,
			},
		},
	}, err
}
