// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/internal/agent/logging"
	mcptools "github.com/azure/azure-dev/cli/azd/internal/agent/tools/mcp"
	azdmcp "github.com/azure/azure-dev/cli/azd/internal/mcp"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
)

// pluginSpec defines a required plugin with its install source and installed name.
type pluginSpec struct {
	// Source is the install path (e.g., "microsoft/GitHub-Copilot-for-Azure:plugin")
	Source string
	// Name is the installed plugin name used for update (e.g., "azure")
	Name string
}

// requiredPlugins lists plugins that must be installed before starting a Copilot session.
var requiredPlugins = []pluginSpec{
	{Source: "microsoft/GitHub-Copilot-for-Azure:plugin", Name: "azure"},
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

	// Create file logger for session event audit trail
	fileLogger, fileLoggerCleanup, err := logging.NewSessionFileLogger()
	if err != nil {
		defer cleanup()
		return nil, fmt.Errorf("failed to create session file logger: %w", err)
	}
	cleanupTasks["fileLogger"] = fileLoggerCleanup

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

	// Wire permission handler — approve CLI-level permission requests.
	// Fine-grained tool consent is handled by OnPreToolUse hook below.
	sessionConfig.OnPermissionRequest = f.createPermissionHandler()

	// Wire user input handler — enables the agent's ask_user tool.
	// Questions are rendered using azd's UX prompts (Select, Prompt).
	sessionConfig.OnUserInputRequest = f.createUserInputHandler(ctx)

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

	// Subscribe file logger to session events for audit trail
	// UX rendering is handled by AgentDisplay in CopilotAgent.SendMessage()
	unsubscribe := session.On(func(event copilot.SessionEvent) {
		fileLogger.HandleEvent(event)
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
		WithCopilotCleanup(cleanup),
	}
	allOpts = append(allOpts, opts...)

	agent := NewCopilotAgent(session, f.console, allOpts...)

	return agent, nil
}

// ensurePlugins checks required plugins and installs or updates them.
func (f *CopilotAgentFactory) ensurePlugins(ctx context.Context) error {
	cliPath := f.clientManager.CLIPath()
	if cliPath == "" {
		cliPath = "copilot"
	}

	// Get list of installed plugins
	installed := f.getInstalledPlugins(ctx, cliPath)

	for _, plugin := range requiredPlugins {
		if installed[plugin.Name] {
			// Already installed — update to latest
			log.Printf("[copilot] Updating plugin: %s", plugin.Name)
			cmd := exec.CommandContext(ctx, cliPath, "plugin", "update", plugin.Name)
			out, err := cmd.CombinedOutput()
			if err != nil {
				log.Printf("[copilot] Plugin update warning for %s: %v (output: %s)",
					plugin.Name, err, string(out))
			} else {
				log.Printf("[copilot] Plugin updated: %s", plugin.Name)
			}
		} else {
			// Not installed — full install
			log.Printf("[copilot] Installing plugin: %s", plugin.Source)
			cmd := exec.CommandContext(ctx, cliPath, "plugin", "install", plugin.Source)
			out, err := cmd.CombinedOutput()
			if err != nil {
				log.Printf("[copilot] Plugin install warning for %s: %v (output: %s)",
					plugin.Source, err, string(out))
			} else {
				log.Printf("[copilot] Plugin installed: %s", plugin.Name)
			}
		}
	}

	return nil
}

// getInstalledPlugins returns a set of installed plugin names.
func (f *CopilotAgentFactory) getInstalledPlugins(ctx context.Context, cliPath string) map[string]bool {
	cmd := exec.CommandContext(ctx, cliPath, "plugin", "list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[copilot] Failed to list plugins: %v", err)
		return nil
	}

	installed := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		// Lines look like: "  • azure (v1.0.0)"
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "•") || strings.HasPrefix(line, "\u2022") {
			name := strings.TrimPrefix(line, "•")
			name = strings.TrimPrefix(name, "\u2022")
			name = strings.TrimSpace(name)
			// Strip version suffix: "azure (v1.0.0)" → "azure"
			if idx := strings.Index(name, " "); idx > 0 {
				name = name[:idx]
			}
			if name != "" {
				installed[name] = true
			}
		}
	}

	return installed
}

// createPermissionHandler builds an OnPermissionRequest handler.
// This handles the CLI's coarse-grained permission requests (file access, shell, URLs).
// We approve all here — fine-grained tool consent is handled by OnPreToolUse.
func (f *CopilotAgentFactory) createPermissionHandler() copilot.PermissionHandlerFunc {
	return func(req copilot.PermissionRequest, inv copilot.PermissionInvocation) (
		copilot.PermissionRequestResult, error,
	) {
		log.Printf("[copilot] PermissionRequest: kind=%s — approved", req.Kind)
		return copilot.PermissionRequestResult{Kind: "approved"}, nil
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

// createUserInputHandler builds an OnUserInputRequest handler that renders
// agent questions using azd's UX prompt components (Select for choices, Prompt for freeform).
func (f *CopilotAgentFactory) createUserInputHandler(
	ctx context.Context,
) copilot.UserInputHandler {
	return func(req copilot.UserInputRequest, inv copilot.UserInputInvocation) (
		copilot.UserInputResponse, error,
	) {
		// Strip markdown from question and choices for clean terminal prompts
		question := stripMarkdown(req.Question)
		log.Printf("[copilot] UserInput: question=%q choices=%d", question, len(req.Choices))

		fmt.Println() // blank line before prompt

		if len(req.Choices) > 0 {
			// Multiple choice — use azd Select prompt
			choices := make([]*uxlib.SelectChoice, len(req.Choices))
			for i, c := range req.Choices {
				plain := stripMarkdown(c)
				choices[i] = &uxlib.SelectChoice{Value: c, Label: plain}
			}

			// If freeform is allowed alongside choices, add an "Other" option
			allowFreeform := req.AllowFreeform != nil && *req.AllowFreeform
			freeformValue := "__freeform__"
			if allowFreeform {
				choices = append(choices, &uxlib.SelectChoice{
					Value: freeformValue,
					Label: "Other (type your own answer)",
				})
			}

			selector := uxlib.NewSelect(&uxlib.SelectOptions{
				Message:         question,
				Choices:         choices,
				EnableFiltering: uxlib.Ptr(false),
				DisplayCount:    min(len(choices), 10),
			})

			idx, err := selector.Ask(ctx)
			fmt.Println() // blank line after prompt
			if err != nil {
				return copilot.UserInputResponse{}, fmt.Errorf("user input cancelled: %w", err)
			}
			if idx == nil || *idx < 0 || *idx >= len(choices) {
				return copilot.UserInputResponse{}, fmt.Errorf("invalid selection")
			}

			selected := choices[*idx].Value
			if selected == freeformValue {
				// User chose freeform — prompt for text input
				prompt := uxlib.NewPrompt(&uxlib.PromptOptions{
					Message: question,
				})
				answer, promptErr := prompt.Ask(ctx)
				fmt.Println() // blank line after prompt
				if promptErr != nil {
					return copilot.UserInputResponse{}, fmt.Errorf("user input cancelled: %w", promptErr)
				}
				log.Printf("[copilot] UserInput: freeform=%q", answer)
				return copilot.UserInputResponse{Answer: answer, WasFreeform: true}, nil
			}

			log.Printf("[copilot] UserInput: selected=%q", selected)
			return copilot.UserInputResponse{Answer: selected}, nil
		}

		// Freeform text input — use azd Prompt
		prompt := uxlib.NewPrompt(&uxlib.PromptOptions{
			Message: question,
		})

		answer, err := prompt.Ask(ctx)
		fmt.Println() // blank line after prompt
		if err != nil {
			return copilot.UserInputResponse{}, fmt.Errorf("user input cancelled: %w", err)
		}

		log.Printf("[copilot] UserInput: freeform=%q", answer)
		return copilot.UserInputResponse{Answer: answer, WasFreeform: true}, nil
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

// stripMarkdown removes common markdown formatting for clean terminal display.
func stripMarkdown(s string) string {
	s = strings.TrimSpace(s)

	// Remove bold/italic markers
	for _, marker := range []string{"***", "**", "*", "___", "__", "_"} {
		s = strings.ReplaceAll(s, marker, "")
	}

	// Remove inline code backticks
	s = strings.ReplaceAll(s, "`", "")

	// Remove heading markers at line starts
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		for _, prefix := range []string{"###### ", "##### ", "#### ", "### ", "## ", "# "} {
			if strings.HasPrefix(trimmed, prefix) {
				lines[i] = strings.TrimPrefix(trimmed, prefix)
				break
			}
		}
	}

	return strings.Join(lines, "\n")
}
