// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
	azdMcp "github.com/azure/azure-dev/cli/azd/internal/mcp"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// pluginSpec defines a required plugin with its install source and installed name.
type pluginSpec struct {
	Source string
	Name   string
}

// requiredPlugins lists plugins that must be installed before starting a Copilot session.
var requiredPlugins = []pluginSpec{
	{Source: "microsoft/GitHub-Copilot-for-Azure:plugin", Name: "azure"},
}

// CopilotAgentFactory creates CopilotAgent instances with all dependencies wired.
// Designed for IoC injection — register with container, inject into commands.
type CopilotAgentFactory struct {
	clientManager        *agentcopilot.CopilotClientManager
	sessionConfigBuilder *agentcopilot.SessionConfigBuilder
	consentManager       consent.ConsentManager
	console              input.Console
	configManager        config.UserConfigManager
}

// NewCopilotAgentFactory creates a new factory.
func NewCopilotAgentFactory(
	clientManager *agentcopilot.CopilotClientManager,
	sessionConfigBuilder *agentcopilot.SessionConfigBuilder,
	consentManager consent.ConsentManager,
	console input.Console,
	configManager config.UserConfigManager,
) *CopilotAgentFactory {
	return &CopilotAgentFactory{
		clientManager:        clientManager,
		sessionConfigBuilder: sessionConfigBuilder,
		consentManager:       consentManager,
		console:              console,
		configManager:        configManager,
	}
}

// Create builds a new CopilotAgent with all dependencies wired.
// Use AgentOption functions to override model, reasoning, mode, etc.
func (f *CopilotAgentFactory) Create(ctx context.Context, opts ...AgentOption) (*CopilotAgent, error) {
	agent := &CopilotAgent{
		clientManager:        f.clientManager,
		sessionConfigBuilder: f.sessionConfigBuilder,
		consentManager:       f.consentManager,
		console:              f.console,
		configManager:        f.configManager,
		mode:                 AgentModeInteractive,
	}

	for _, opt := range opts {
		opt(agent)
	}

	return agent, nil
}

// loadBuiltInMCPServers loads the embedded mcp.json configuration.
func loadBuiltInMCPServers() (map[string]*azdMcp.ServerConfig, error) {
	var mcpConfig *azdMcp.McpConfig
	if err := json.Unmarshal([]byte(mcptools.McpJson), &mcpConfig); err != nil {
		return nil, fmt.Errorf("failed parsing embedded mcp.json: %w", err)
	}
	return mcpConfig.Servers, nil
}
