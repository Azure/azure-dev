// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"context"
	"fmt"
	"log"

	copilot "github.com/github/copilot-sdk/go"
)

// CopilotClientManager manages the lifecycle of a Copilot SDK client.
// It wraps copilot.Client with azd-specific configuration and error handling.
type CopilotClientManager struct {
	client  *copilot.Client
	options *CopilotClientOptions
}

// CopilotClientOptions configures the CopilotClientManager.
type CopilotClientOptions struct {
	// LogLevel controls SDK logging verbosity (e.g., "info", "debug", "error").
	LogLevel string
}

// NewCopilotClientManager creates a new CopilotClientManager with the given options.
// If options is nil, defaults are used.
func NewCopilotClientManager(options *CopilotClientOptions) *CopilotClientManager {
	if options == nil {
		options = &CopilotClientOptions{}
	}

	clientOpts := &copilot.ClientOptions{}
	if options.LogLevel != "" {
		clientOpts.LogLevel = options.LogLevel
	} else {
		clientOpts.LogLevel = "debug"
	}

	return &CopilotClientManager{
		client:  copilot.NewClient(clientOpts),
		options: options,
	}
}

// Start initializes the Copilot SDK client and establishes a connection
// to the copilot-agent-runtime process.
func (m *CopilotClientManager) Start(ctx context.Context) error {
	log.Printf("[copilot-client] Starting client (logLevel=%q)...", m.options.LogLevel)
	log.Printf("[copilot-client] SDK will spawn copilot CLI process via stdio transport")
	if err := m.client.Start(ctx); err != nil {
		log.Printf("[copilot-client] Start failed: %v", err)
		log.Printf("[copilot-client] Ensure 'copilot' CLI is in PATH and supports SDK protocol")
		return fmt.Errorf(
			"failed to start Copilot agent runtime: %w",
			err,
		)
	}
	log.Printf("[copilot-client] Started successfully (state=%s)", m.client.State())
	return nil
}

// Stop gracefully shuts down the Copilot SDK client and terminates the agent runtime process.
func (m *CopilotClientManager) Stop() error {
	if m.client == nil {
		return nil
	}
	return m.client.Stop()
}

// Client returns the underlying copilot.Client for session creation.
func (m *CopilotClientManager) Client() *copilot.Client {
	return m.client
}

// GetAuthStatus checks whether the user is authenticated with GitHub Copilot.
func (m *CopilotClientManager) GetAuthStatus(ctx context.Context) (*copilot.GetAuthStatusResponse, error) {
	status, err := m.client.GetAuthStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check Copilot auth status: %w", err)
	}
	return status, nil
}

// ListModels returns the list of available models from the Copilot service.
func (m *CopilotClientManager) ListModels(ctx context.Context) ([]copilot.ModelInfo, error) {
	models, err := m.client.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list Copilot models: %w", err)
	}
	return models, nil
}

// State returns the current connection state of the client.
func (m *CopilotClientManager) State() copilot.ConnectionState {
	return m.client.State()
}
