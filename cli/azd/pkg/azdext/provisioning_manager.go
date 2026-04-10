// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

// ProvisioningProvider defines the interface for provisioning logic on the extension side.
type ProvisioningProvider interface {
	Initialize(ctx context.Context, projectPath string, options *ProvisioningOptions) error
	State(ctx context.Context, options *ProvisioningStateOptions) (*ProvisioningStateResult, error)
	Deploy(ctx context.Context, progress grpcbroker.ProgressFunc) (*ProvisioningDeployResult, error)
	Preview(ctx context.Context, progress grpcbroker.ProgressFunc) (*ProvisioningPreviewResult, error)
	Destroy(
		ctx context.Context,
		options *ProvisioningDestroyOptions,
		progress grpcbroker.ProgressFunc,
	) (*ProvisioningDestroyResult, error)
	EnsureEnv(ctx context.Context) error
	Parameters(ctx context.Context) ([]*ProvisioningParameter, error)
	PlannedOutputs(ctx context.Context) ([]*ProvisioningPlannedOutput, error)
}

// ProvisioningProviderFactory describes a function that creates a provisioning provider instance.
type ProvisioningProviderFactory func() ProvisioningProvider

// ProvisioningManager handles registration and provisioning request forwarding for a provider.
type ProvisioningManager struct {
	extensionId  string
	client       *AzdClient
	broker       *grpcbroker.MessageBroker[ProvisioningMessage]
	provider     ProvisioningProvider
	providerName string
	brokerLogger *log.Logger

	// Synchronization for concurrent access
	mu sync.RWMutex
}

// NewProvisioningManager creates a new ProvisioningManager for an AzdClient.
func NewProvisioningManager(
	extensionId string,
	client *AzdClient,
	brokerLogger *log.Logger,
) *ProvisioningManager {
	return &ProvisioningManager{
		extensionId:  extensionId,
		client:       client,
		brokerLogger: brokerLogger,
	}
}

// Close terminates the underlying gRPC stream if it's been initialized.
// This method is thread-safe for concurrent access.
func (m *ProvisioningManager) Close() error {
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
func (m *ProvisioningManager) ensureStream(ctx context.Context) error {
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

	stream, err := m.client.Provisioning().Stream(ctx)
	if err != nil {
		return fmt.Errorf("failed to create provisioning stream: %w", err)
	}

	// Create broker with client stream
	envelope := &ProvisioningEnvelope{}
	m.broker = grpcbroker.NewMessageBroker(stream, envelope, m.extensionId, m.brokerLogger)

	// Register handlers for incoming requests
	if err := m.broker.On(m.onInitialize); err != nil {
		return fmt.Errorf("failed to register initialize handler: %w", err)
	}
	if err := m.broker.On(m.onState); err != nil {
		return fmt.Errorf("failed to register state handler: %w", err)
	}
	if err := m.broker.On(m.onDeploy); err != nil {
		return fmt.Errorf("failed to register deploy handler: %w", err)
	}
	if err := m.broker.On(m.onPreview); err != nil {
		return fmt.Errorf("failed to register preview handler: %w", err)
	}
	if err := m.broker.On(m.onDestroy); err != nil {
		return fmt.Errorf("failed to register destroy handler: %w", err)
	}
	if err := m.broker.On(m.onEnsureEnv); err != nil {
		return fmt.Errorf("failed to register ensure env handler: %w", err)
	}
	if err := m.broker.On(m.onParameters); err != nil {
		return fmt.Errorf("failed to register parameters handler: %w", err)
	}
	if err := m.broker.On(m.onPlannedOutputs); err != nil {
		return fmt.Errorf("failed to register planned outputs handler: %w", err)
	}

	return nil
}

// Register registers the provider with the server, waits for the response,
// then starts background handling of provisioning requests.
func (m *ProvisioningManager) Register(
	ctx context.Context,
	factory ProvisioningProviderFactory,
	providerName string,
) error {
	if strings.TrimSpace(providerName) == "" {
		return fmt.Errorf("provisioning provider name cannot be empty")
	}

	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	registerReq := &ProvisioningMessage{
		RequestId: uuid.NewString(),
		MessageType: &ProvisioningMessage_RegisterProvisioningProviderRequest{
			RegisterProvisioningProviderRequest: &RegisterProvisioningProviderRequest{
				Name: providerName,
			},
		},
	}

	resp, err := m.broker.SendAndWait(ctx, registerReq)
	if err != nil {
		return fmt.Errorf(
			"provisioning provider registration failed: %w", err,
		)
	}

	if resp.GetRegisterProvisioningProviderResponse() == nil {
		return fmt.Errorf(
			"expected RegisterProvisioningProviderResponse, got %T",
			resp.GetMessageType(),
		)
	}

	// Create provider and store with provider name in a single lock scope
	provider := factory()

	m.mu.Lock()
	m.providerName = providerName
	m.provider = provider
	m.mu.Unlock()

	return nil
}

// Receive starts the broker's message dispatcher and blocks until the stream completes.
func (m *ProvisioningManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	return m.broker.Run(ctx)
}

// Ready blocks until the message broker starts receiving messages or the context is cancelled.
func (m *ProvisioningManager) Ready(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return err
	}

	return m.broker.Ready(ctx)
}

// Handler methods - these are registered with the broker to handle
// incoming requests

// getProvider returns the provisioning provider, or an error if
// not initialized.
func (m *ProvisioningManager) getProvider() (
	ProvisioningProvider, error,
) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.provider == nil {
		return nil, fmt.Errorf(
			"provisioning provider not initialized",
		)
	}
	return m.provider, nil
}

// onInitialize handles initialization requests from the server
func (m *ProvisioningManager) onInitialize(
	ctx context.Context,
	req *ProvisioningInitializeRequest,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider()
	if err != nil {
		return nil, err
	}

	err = provider.Initialize(
		ctx, req.GetProjectPath(), req.GetOptions(),
	)

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_InitializeResponse{
			InitializeResponse: &ProvisioningInitializeResponse{},
		},
	}, err
}

// onState handles state requests
func (m *ProvisioningManager) onState(
	ctx context.Context,
	req *ProvisioningStateRequest,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider()
	if err != nil {
		return nil, err
	}

	result, err := provider.State(ctx, req.GetOptions())
	if err != nil {
		return nil, err
	}

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_StateResponse{
			StateResponse: &ProvisioningStateResponse{
				StateResult: result,
			},
		},
	}, nil
}

// onDeploy handles deploy requests with progress reporting
func (m *ProvisioningManager) onDeploy(
	ctx context.Context,
	req *ProvisioningDeployRequest,
	progress grpcbroker.ProgressFunc,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider()
	if err != nil {
		return nil, err
	}

	result, err := provider.Deploy(ctx, progress)
	if err != nil {
		return nil, err
	}

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_DeployResponse{
			DeployResponse: &ProvisioningDeployResponse{
				Result: result,
			},
		},
	}, nil
}

// onPreview handles preview requests with progress reporting
func (m *ProvisioningManager) onPreview(
	ctx context.Context,
	req *ProvisioningPreviewRequest,
	progress grpcbroker.ProgressFunc,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider()
	if err != nil {
		return nil, err
	}

	result, err := provider.Preview(ctx, progress)
	if err != nil {
		return nil, err
	}

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_PreviewResponse{
			PreviewResponse: &ProvisioningPreviewResponse{
				Result: result,
			},
		},
	}, nil
}

// onDestroy handles destroy requests with progress reporting
func (m *ProvisioningManager) onDestroy(
	ctx context.Context,
	req *ProvisioningDestroyRequest,
	progress grpcbroker.ProgressFunc,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider()
	if err != nil {
		return nil, err
	}

	result, err := provider.Destroy(
		ctx, req.GetOptions(), progress,
	)
	if err != nil {
		return nil, err
	}

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_DestroyResponse{
			DestroyResponse: &ProvisioningDestroyResponse{
				Result: result,
			},
		},
	}, nil
}

// onEnsureEnv handles ensure env requests
func (m *ProvisioningManager) onEnsureEnv(
	ctx context.Context,
	req *ProvisioningEnsureEnvRequest,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider()
	if err != nil {
		return nil, err
	}

	err = provider.EnsureEnv(ctx)

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_EnsureEnvResponse{
			EnsureEnvResponse: &ProvisioningEnsureEnvResponse{},
		},
	}, err
}

// onParameters handles parameters requests
func (m *ProvisioningManager) onParameters(
	ctx context.Context,
	req *ProvisioningParametersRequest,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider()
	if err != nil {
		return nil, err
	}

	params, err := provider.Parameters(ctx)
	if err != nil {
		return nil, err
	}

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_ParametersResponse{
			ParametersResponse: &ProvisioningParametersResponse{
				Parameters: params,
			},
		},
	}, nil
}

// onPlannedOutputs handles planned outputs requests
func (m *ProvisioningManager) onPlannedOutputs(
	ctx context.Context,
	req *ProvisioningPlannedOutputsRequest,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider()
	if err != nil {
		return nil, err
	}

	outputs, err := provider.PlannedOutputs(ctx)
	if err != nil {
		return nil, err
	}

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_PlannedOutputsResponse{
			PlannedOutputsResponse: &ProvisioningPlannedOutputsResponse{
				PlannedOutputs: outputs,
			},
		},
	}, nil
}
