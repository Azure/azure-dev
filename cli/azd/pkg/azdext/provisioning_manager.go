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
//
// Note: Deploy, Preview, and Destroy include a progress callback parameter that the core
// provisioning.Provider interface does not have. This is intentional — extension providers
// use in-band progress streaming via the gRPC broker, while core providers report progress
// through separate channels managed by the provisioning Manager.
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

// ProvisioningProviderFactory creates a new ProvisioningProvider instance.
type ProvisioningProviderFactory func() ProvisioningProvider

// ProvisioningManager manages provisioning provider instances using a two-map
// pattern (factories + instances). Unlike ServiceTargetManager which delegates
// to ComponentManager[T], ProvisioningManager uses direct maps because
// ProvisioningProvider.Initialize() has a different signature (projectPath +
// ProvisioningOptions) than ComponentManager's Provider interface
// (ServiceConfig). The routing key is provider name (from ProvisioningOptions.Provider)
// rather than service config host.
type ProvisioningManager struct {
	extensionId  string
	client       *AzdClient
	broker       *grpcbroker.MessageBroker[ProvisioningMessage]
	brokerLogger *log.Logger

	// factories maps provider names to their factory functions (registered via Register).
	factories map[string]ProvisioningProviderFactory
	// instances maps provider names to initialized provider instances (created on Initialize).
	instances map[string]ProvisioningProvider

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
		factories:    make(map[string]ProvisioningProviderFactory),
		instances:    make(map[string]ProvisioningProvider),
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

	clear(m.factories)
	clear(m.instances)

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

	// Register handlers for incoming requests; clean up broker on failure
	if err := m.registerHandlers(); err != nil {
		m.broker.Close()
		m.broker = nil
		return fmt.Errorf("failed to register provisioning handlers: %w", err)
	}

	return nil
}

// registerHandlers registers all provisioning message handlers with the broker.
func (m *ProvisioningManager) registerHandlers() error {
	handlers := []any{
		m.onInitialize,
		m.onState,
		m.onDeploy,
		m.onPreview,
		m.onDestroy,
		m.onEnsureEnv,
		m.onParameters,
		m.onPlannedOutputs,
	}
	for _, h := range handlers {
		if err := m.broker.On(h); err != nil {
			return err
		}
	}
	return nil
}

// Register registers a provisioning provider factory with the server, waits for the response,
// then starts background handling of provisioning requests.
// Multiple providers can be registered per extension; calling Register with the same name twice returns an error.
func (m *ProvisioningManager) Register(
	ctx context.Context,
	factory ProvisioningProviderFactory,
	providerName string,
) error {
	if strings.TrimSpace(providerName) == "" {
		return fmt.Errorf("provisioning provider name cannot be empty")
	}

	if err := m.ensureStream(ctx); err != nil {
		return fmt.Errorf("failed to ensure provisioning stream for registration: %w", err)
	}

	// Check for duplicate BEFORE sending
	m.mu.RLock()
	if _, exists := m.factories[providerName]; exists {
		m.mu.RUnlock()
		return fmt.Errorf(
			"provisioning provider '%s' already registered",
			providerName,
		)
	}
	m.mu.RUnlock()

	// Send registration to server
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

	if resp == nil {
		return fmt.Errorf("provisioning provider registration: received nil response")
	}

	if resp.GetRegisterProvisioningProviderResponse() == nil {
		return fmt.Errorf(
			"expected RegisterProvisioningProviderResponse, got %T",
			resp.GetMessageType(),
		)
	}

	// Store factory ONLY after successful registration
	m.mu.Lock()
	m.factories[providerName] = factory
	m.mu.Unlock()

	return nil
}

// Receive starts the broker's message dispatcher and blocks until the stream completes.
func (m *ProvisioningManager) Receive(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return fmt.Errorf("failed to ensure provisioning stream for receiving: %w", err)
	}

	return m.broker.Run(ctx)
}

// Ready blocks until the message broker starts receiving messages or the context is cancelled.
func (m *ProvisioningManager) Ready(ctx context.Context) error {
	if err := m.ensureStream(ctx); err != nil {
		return fmt.Errorf("failed to ensure provisioning stream for readiness check: %w", err)
	}

	return m.broker.Ready(ctx)
}

// Handler methods - these are registered with the broker to handle
// incoming requests

// getOrCreateProvider looks up or creates a provider instance by name.
// It uses the factory to create a new instance and calls Initialize on it.
func (m *ProvisioningManager) getOrCreateProvider(
	ctx context.Context,
	providerName string,
	projectPath string,
	options *ProvisioningOptions,
) (ProvisioningProvider, error) {
	// Fast path: check if already initialized
	m.mu.RLock()
	if instance, exists := m.instances[providerName]; exists {
		m.mu.RUnlock()
		return instance, nil
	}
	m.mu.RUnlock()

	// Slow path: create and initialize
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock
	if instance, exists := m.instances[providerName]; exists {
		return instance, nil
	}

	factory, exists := m.factories[providerName]
	if !exists {
		return nil, fmt.Errorf(
			"no factory registered for provisioning provider '%s'",
			providerName,
		)
	}

	provider := factory()
	if err := provider.Initialize(ctx, projectPath, options); err != nil {
		return nil, fmt.Errorf(
			"failed to initialize provisioning provider '%s': %w",
			providerName, err,
		)
	}

	m.instances[providerName] = provider
	return provider, nil
}

// getProvider retrieves an already-initialized provider by name.
// Returns an error if the provider has not been initialized yet.
func (m *ProvisioningManager) getProvider(
	providerName string,
) (ProvisioningProvider, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	instance, exists := m.instances[providerName]
	if !exists {
		return nil, fmt.Errorf(
			"provisioning provider '%s' not initialized; "+
				"Initialize must be called first",
			providerName,
		)
	}
	return instance, nil
}

// onInitialize handles initialization requests from the server.
// provider_name is extracted from ProvisioningOptions.provider (not a separate
// field) because Initialize is the first call and Options always contains the
// provider identity. This matches how the core Manager.newProvider() resolves
// by Options.Provider.
func (m *ProvisioningManager) onInitialize(
	ctx context.Context,
	req *ProvisioningInitializeRequest,
) (*ProvisioningMessage, error) {
	providerName := ""
	if req.GetOptions() != nil {
		providerName = req.GetOptions().GetProvider()
	}
	if providerName == "" {
		return nil, fmt.Errorf(
			"provider name is required in ProvisioningOptions " +
				"for initialization",
		)
	}

	_, err := m.getOrCreateProvider(
		ctx, providerName, req.GetProjectPath(), req.GetOptions(),
	)
	if err != nil {
		return nil, err
	}

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_InitializeResponse{
			InitializeResponse: &ProvisioningInitializeResponse{},
		},
	}, nil
}

// onState handles state requests
func (m *ProvisioningManager) onState(
	ctx context.Context,
	req *ProvisioningStateRequest,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider(req.GetProviderName())
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
	provider, err := m.getProvider(req.GetProviderName())
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
	provider, err := m.getProvider(req.GetProviderName())
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
	provider, err := m.getProvider(req.GetProviderName())
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
	provider, err := m.getProvider(req.GetProviderName())
	if err != nil {
		return nil, err
	}

	if err := provider.EnsureEnv(ctx); err != nil {
		return nil, err
	}

	return &ProvisioningMessage{
		MessageType: &ProvisioningMessage_EnsureEnvResponse{
			EnsureEnvResponse: &ProvisioningEnsureEnvResponse{},
		},
	}, nil
}

// onParameters handles parameters requests
func (m *ProvisioningManager) onParameters(
	ctx context.Context,
	req *ProvisioningParametersRequest,
) (*ProvisioningMessage, error) {
	provider, err := m.getProvider(req.GetProviderName())
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
	provider, err := m.getProvider(req.GetProviderName())
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
