// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
	azdmcp "github.com/azure/azure-dev/cli/azd/internal/mcp"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
)

// requiredPlugins lists plugins that must be installed before starting a Copilot session.
var requiredPlugins = []string{
	"microsoft/GitHub-Copilot-for-Azure:plugin",
}

// CopilotAgentFactory creates CopilotAgent instances using the GitHub Copilot SDK.
// It manages the Copilot client lifecycle, MCP server configuration, and session hooks.
type CopilotAgentFactory struct {
	clientManager        *llm.CopilotClientManager
	sessionConfigBuilder *llm.SessionConfigBuilder
	consentManager       consent.ConsentManager
	console              input.Console
}

// NewCopilotAgentFactory creates a new factory for building Copilot SDK-based agents.
func NewCopilotAgentFactory(
	clientManager *llm.CopilotClientManager,
	sessionConfigBuilder *llm.SessionConfigBuilder,
	consentManager consent.ConsentManager,
	console input.Console,
) *CopilotAgentFactory {
	return &CopilotAgentFactory{
		clientManager:        clientManager,
		sessionConfigBuilder: sessionConfigBuilder,
		consentManager:       consentManager,
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

	// Ensure required plugins are installed
	if err := f.ensurePlugins(ctx); err != nil {
		log.Printf("[copilot] Warning: plugin installation issue: %v", err)
	}

	// Start the Copilot client (spawns copilot-agent-runtime)
	log.Println("[copilot] Starting Copilot SDK client...")
	if err := f.clientManager.Start(ctx); err != nil {
		return nil, err
	}
	log.Printf("[copilot] Client started (state: %s)", f.clientManager.State())
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
	log.Printf("[copilot] Loaded %d built-in MCP servers", len(builtInServers))

	// Build session config from azd user config
	sessionConfig, err := f.sessionConfigBuilder.Build(ctx, builtInServers)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("failed to build session config: %w", err)
	}
	log.Printf("[copilot] Session config built (model=%q, mcpServers=%d, availableTools=%d, excludedTools=%d)",
		sessionConfig.Model, len(sessionConfig.MCPServers),
		len(sessionConfig.AvailableTools), len(sessionConfig.ExcludedTools))

	// Wire permission handler — delegates to azd consent system
	sessionConfig.OnPermissionRequest = f.createPermissionHandler(ctx)

	// Wire lifecycle hooks — PreToolUse delegates to azd consent system
	sessionConfig.Hooks = &copilot.SessionHooks{
		OnPreToolUse:  f.createPreToolUseHandler(ctx),
		OnPostToolUse: f.createPostToolUseHandler(),
		OnErrorOccurred: func(input copilot.ErrorOccurredHookInput, inv copilot.HookInvocation) (
			*copilot.ErrorOccurredHookOutput, error,
		) {
			log.Printf("[copilot] ErrorOccurred: error=%s recoverable=%v", input.Error, input.Recoverable)
			return nil, nil
		},
	}

	// Create session
	log.Println("[copilot] Creating session...")
	session, err := f.clientManager.Client().CreateSession(ctx, sessionConfig)
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("failed to create Copilot session: %w", err)
	}
	log.Println("[copilot] Session created successfully")

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

// ensurePlugins installs required and user-configured plugins if not already present.
func (f *CopilotAgentFactory) ensurePlugins(ctx context.Context) error {
	cliPath := f.clientManager.CLIPath()
	if cliPath == "" {
		cliPath = "copilot"
	}

	for _, plugin := range requiredPlugins {
		log.Printf("[copilot] Ensuring plugin installed: %s", plugin)
		cmd := exec.CommandContext(ctx, cliPath, "plugin", "install", plugin)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("[copilot] Plugin install warning for %s: %v (output: %s)", plugin, err, string(output))
		} else {
			log.Printf("[copilot] Plugin ready: %s", plugin)
		}
	}

	return nil
}

// createPermissionHandler builds an OnPermissionRequest handler that delegates
// to the azd consent system for approval decisions.
func (f *CopilotAgentFactory) createPermissionHandler(
	ctx context.Context,
) copilot.PermissionHandlerFunc {
	return func(req copilot.PermissionRequest, inv copilot.PermissionInvocation) (
		copilot.PermissionRequestResult, error,
	) {
		log.Printf("[copilot] PermissionRequest: kind=%s", req.Kind)

		// Build a consent request from the SDK permission request
		consentReq := consent.ConsentRequest{
			ToolID:     req.Kind,
			ServerName: "copilot",
			Operation:  consent.OperationTypeTool,
		}

		decision, err := f.consentManager.CheckConsent(ctx, consentReq)
		if err != nil {
			log.Printf("[copilot] Consent check error: %v, approving by default", err)
			return copilot.PermissionRequestResult{Kind: "approved"}, nil
		}

		if decision.Allowed {
			return copilot.PermissionRequestResult{Kind: "approved"}, nil
		}

		if decision.RequiresPrompt {
			// Use the azd consent checker to prompt the user
			checker := consent.NewConsentChecker(f.consentManager, "copilot")
			consentDecision, promptErr := checker.CheckToolConsent(
				ctx, req.Kind, fmt.Sprintf("Copilot permission request: %s", req.Kind),
				mcp.ToolAnnotation{},
			)
			if promptErr != nil {
				return copilot.PermissionRequestResult{Kind: "denied"}, nil
			}

			if consentDecision.Allowed {
				return copilot.PermissionRequestResult{Kind: "approved"}, nil
			}
		}

		return copilot.PermissionRequestResult{Kind: "denied"}, nil
	}
}

// createPreToolUseHandler builds an OnPreToolUse hook that checks the azd
// consent system before each tool execution.
func (f *CopilotAgentFactory) createPreToolUseHandler(
	ctx context.Context,
) copilot.PreToolUseHandler {
	return func(input copilot.PreToolUseHookInput, inv copilot.HookInvocation) (
		*copilot.PreToolUseHookOutput, error,
	) {
		log.Printf("[copilot] PreToolUse: tool=%s", input.ToolName)

		consentReq := consent.ConsentRequest{
			ToolID:     fmt.Sprintf("copilot/%s", input.ToolName),
			ServerName: "copilot",
			Operation:  consent.OperationTypeTool,
		}

		decision, err := f.consentManager.CheckConsent(ctx, consentReq)
		if err != nil {
			log.Printf("[copilot] Consent check error for tool %s: %v, allowing", input.ToolName, err)
			return &copilot.PreToolUseHookOutput{PermissionDecision: "allow"}, nil
		}

		if decision.Allowed {
			return &copilot.PreToolUseHookOutput{PermissionDecision: "allow"}, nil
		}

		if decision.RequiresPrompt {
			// Prompt user via azd consent UX
			checker := consent.NewConsentChecker(f.consentManager, "copilot")
			promptErr := checker.PromptAndGrantConsent(
				ctx, input.ToolName, input.ToolName,
				mcp.ToolAnnotation{},
			)
			if promptErr != nil {
				if promptErr == consent.ErrToolExecutionDenied {
					return &copilot.PreToolUseHookOutput{
						PermissionDecision:       "deny",
						PermissionDecisionReason: "tool execution denied by user",
					}, nil
				}
				log.Printf("[copilot] Consent prompt error for tool %s: %v", input.ToolName, promptErr)
				return &copilot.PreToolUseHookOutput{
					PermissionDecision:       "deny",
					PermissionDecisionReason: "consent prompt failed",
				}, nil
			}

			return &copilot.PreToolUseHookOutput{PermissionDecision: "allow"}, nil
		}

		return &copilot.PreToolUseHookOutput{
			PermissionDecision:       "deny",
			PermissionDecisionReason: decision.Reason,
		}, nil
	}
}

// createPostToolUseHandler builds an OnPostToolUse hook for logging.
func (f *CopilotAgentFactory) createPostToolUseHandler() copilot.PostToolUseHandler {
	return func(input copilot.PostToolUseHookInput, inv copilot.HookInvocation) (
		*copilot.PostToolUseHookOutput, error,
	) {
		log.Printf("[copilot] PostToolUse: tool=%s", input.ToolName)
		return nil, nil
	}
}

// loadBuiltInMCPServers loads the embedded mcp.json configuration.
func loadBuiltInMCPServers() (map[string]*azdmcp.ServerConfig, error) {
	var mcpConfig *azdmcp.McpConfig
	if err := json.Unmarshal([]byte(mcptools.McpJson), &mcpConfig); err != nil {
		return nil, fmt.Errorf("failed parsing embedded mcp.json: %w", err)
	}
	return mcpConfig.Servers, nil
}
