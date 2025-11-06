// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"errors"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

var (
	FrameworkServiceFactoryKey = func(config *ServiceConfig) string {
		return string(config.Language)
	}
)

// FrameworkServiceProvider defines the interface for framework service logic.
type FrameworkServiceProvider interface {
	Initialize(ctx context.Context, serviceConfig *ServiceConfig) error
	RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) ([]*ExternalTool, error)
	Requirements() (*FrameworkRequirements, error)
	Restore(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress grpcbroker.ProgressFunc,
	) (*ServiceRestoreResult, error)
	Build(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress grpcbroker.ProgressFunc,
	) (*ServiceBuildResult, error)
	Package(
		ctx context.Context,
		serviceConfig *ServiceConfig,
		serviceContext *ServiceContext,
		progress grpcbroker.ProgressFunc,
	) (*ServicePackageResult, error)
}

// FrameworkServiceManager handles registration and request forwarding for a framework service provider.
type FrameworkServiceManager struct {
	client           *AzdClient
	broker           *grpcbroker.MessageBroker[FrameworkServiceMessage]
	componentManager *ComponentManager[FrameworkServiceProvider]
}

// NewFrameworkServiceManager creates a new FrameworkServiceManager for an AzdClient.
func NewFrameworkServiceManager(client *AzdClient) *FrameworkServiceManager {
	return &FrameworkServiceManager{
		client:           client,
		componentManager: NewComponentManager[FrameworkServiceProvider](FrameworkServiceFactoryKey, "framework service"),
	}
}

// Close closes the framework service manager and cleans up resources.
func (m *FrameworkServiceManager) Close() error {
	if m.broker != nil {
		m.broker.Close()
	}
	return m.componentManager.Close()
}

// ensureStream initializes the broker and stream if they haven't been created yet.
func (m *FrameworkServiceManager) ensureStream(ctx context.Context) error {
	if m.broker == nil {
		stream, err := m.client.FrameworkService().Stream(ctx)
		if err != nil {
			return fmt.Errorf("failed to create framework service stream: %w", err)
		}

		// Create broker with client stream
		envelope := &FrameworkServiceEnvelope{}
		m.broker = grpcbroker.NewMessageBroker(stream, envelope)

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
// Returns nil on graceful shutdown, or an error if the stream fails.
func (m *FrameworkServiceManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}
	return m.broker.Run(ctx)
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
	progress grpcbroker.ProgressFunc,
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
	progress grpcbroker.ProgressFunc,
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
	progress grpcbroker.ProgressFunc,
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
