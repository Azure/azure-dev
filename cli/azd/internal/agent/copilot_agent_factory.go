// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
	"github.com/azure/azure-dev/cli/azd/internal/mcp"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
)

// CopilotAgentFactory creates CopilotAgent instances using the GitHub Copilot SDK.
// It manages the Copilot client lifecycle, MCP server configuration, and session hooks.
type CopilotAgentFactory struct {
	clientManager        *llm.CopilotClientManager
	sessionConfigBuilder *llm.SessionConfigBuilder
	console              input.Console
}

// NewCopilotAgentFactory creates a new factory for building Copilot SDK-based agents.
func NewCopilotAgentFactory(
	clientManager *llm.CopilotClientManager,
	sessionConfigBuilder *llm.SessionConfigBuilder,
	console input.Console,
) *CopilotAgentFactory {
	return &CopilotAgentFactory{
		clientManager:        clientManager,
		sessionConfigBuilder: sessionConfigBuilder,
		console:              console,
	}
}

// Create builds a new CopilotAgent with the Copilot SDK session, MCP servers,
// permission hooks, and event handlers configured.
func (f *CopilotAgentFactory) Create(ctx context.Context, opts ...CopilotAgentOption) (Agent, error) {
	cleanupTasks := map[string]func() error{}

	cleanup := func() error {
		for name, task := range cleanupTasks {
			if err := task(); err != nil {
				log.Printf("failed to cleanup %s: %v", name, err)
			}
		}
		return nil
	}

	// Start the Copilot client (spawns copilot-agent-runtime)
	if err := f.clientManager.Start(ctx); err != nil {
		return nil, err
	}
	cleanupTasks["copilot-client"] = f.clientManager.Stop

	// Create thought channel for UX streaming
	thoughtChan := make(chan logging.Thought)
	cleanupTasks["thoughtChan"] = func() error {
		close(thoughtChan)
		return nil
	}

	// Create file logger for session events
	fileLogger, fileLoggerCleanup, err := logging.NewSessionFileLogger()
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("failed to create session file logger: %w", err)
	}
	cleanupTasks["fileLogger"] = fileLoggerCleanup

	// Create event logger for UX thought streaming
	eventLogger := logging.NewSessionEventLogger(thoughtChan)

	// Create composite handler
	compositeHandler := logging.NewCompositeEventHandler(
		eventLogger.HandleEvent,
		fileLogger.HandleEvent,
	)

	// Load built-in MCP server configs
	builtInServers, err := loadBuiltInMCPServers()
	if err != nil {
		defer cleanup()
		return nil, err
	}

	// Build session config from azd user config
	sessionConfig, err := f.sessionConfigBuilder.Build(ctx, builtInServers)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("failed to build session config: %w", err)
	}

	// Wire permission hooks
	sessionConfig.Hooks = &copilot.SessionHooks{
		OnPreToolUse: func(input copilot.PreToolUseHookInput, inv copilot.HookInvocation) (
			*copilot.PreToolUseHookOutput, error,
		) {
			// Allow all tools by default — SDK handles its own permission model.
			// In Phase 2, azd-specific security policies (path validation) will be wired here.
			return &copilot.PreToolUseHookOutput{}, nil
		},
		OnPostToolUse: func(input copilot.PostToolUseHookInput, inv copilot.HookInvocation) (
			*copilot.PostToolUseHookOutput, error,
		) {
			return nil, nil
		},
	}

	// Create session
	session, err := f.clientManager.Client().CreateSession(ctx, sessionConfig)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("failed to create Copilot session: %w", err)
	}

	// Subscribe to session events
	unsubscribe := session.On(func(event copilot.SessionEvent) {
		compositeHandler.HandleEvent(event)
	})

	cleanupTasks["session-events"] = func() error {
		unsubscribe()
		return nil
	}

	cleanupTasks["session"] = func() error {
		return session.Destroy()
	}

	// Build agent options
	allOpts := []CopilotAgentOption{
		WithCopilotThoughtChannel(thoughtChan),
		WithCopilotCleanup(cleanup),
	}
	allOpts = append(allOpts, opts...)

	agent := NewCopilotAgent(session, f.console, allOpts...)

	return agent, nil
}

// loadBuiltInMCPServers loads the embedded mcp.json configuration.
func loadBuiltInMCPServers() (map[string]*mcp.ServerConfig, error) {
	var mcpConfig *mcp.McpConfig
	if err := json.Unmarshal([]byte(mcptools.McpJson), &mcpConfig); err != nil {
		return nil, fmt.Errorf("failed parsing embedded mcp.json: %w", err)
	}
	return mcpConfig.Servers, nil
}
