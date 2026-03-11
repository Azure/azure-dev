// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/llm"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
)

// CopilotAgent is a self-contained agent backed by the GitHub Copilot SDK.
// It encapsulates initialization, session management, display, and usage tracking.
type CopilotAgent struct {
	// Dependencies
	clientManager        *llm.CopilotClientManager
	sessionConfigBuilder *llm.SessionConfigBuilder
	consentManager       consent.ConsentManager
	console              input.Console
	configManager        config.UserConfigManager

	// Configuration overrides (from AgentOption)
	modelOverride           string
	reasoningEffortOverride string
	mode                    AgentMode
	debug                   bool

	// Runtime state
	session   *copilot.Session
	sessionID string
	display   *AgentDisplay // last display for usage metrics

	// Cleanup — ordered slice for deterministic teardown
	cleanupTasks []cleanupTask
}

type cleanupTask struct {
	name string
	fn   func() error
}

// Initialize handles first-run configuration (model/reasoning prompts), plugin install,
// and Copilot client startup. If config already exists, returns current values without
// prompting. Use WithForcePrompt() to always show prompts.
func (a *CopilotAgent) Initialize(ctx context.Context, opts ...InitOption) (*InitResult, error) {
	options := &initOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// Load current config
	azdConfig, err := a.configManager.Load()
	if err != nil {
		return nil, err
	}

	existingModel, hasModel := azdConfig.GetString("ai.agent.model")
	existingEffort, hasEffort := azdConfig.GetString("ai.agent.reasoningEffort")

	// Apply overrides
	if a.modelOverride != "" {
		existingModel = a.modelOverride
		hasModel = true
	}
	if a.reasoningEffortOverride != "" {
		existingEffort = a.reasoningEffortOverride
		hasEffort = true
	}

	// If already configured and not forcing, return current config
	if (hasModel || hasEffort) && !options.forcePrompt {
		return &InitResult{
			Model:           existingModel,
			ReasoningEffort: existingEffort,
			IsFirstRun:      false,
		}, nil
	}

	// First run — prompt for reasoning effort
	effortChoices := []*uxlib.SelectChoice{
		{Value: "low", Label: "Low — fastest, lowest cost"},
		{Value: "medium", Label: "Medium — balanced (recommended)"},
		{Value: "high", Label: "High — more thorough, higher cost and premium requests"},
	}

	effortSelector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message:         "Select reasoning effort level",
		HelpMessage:     "Higher reasoning uses more premium requests and may cost more. You can change this later.",
		Choices:         effortChoices,
		SelectedIndex:   intPtr(1),
		DisplayNumbers:  uxlib.Ptr(true),
		EnableFiltering: uxlib.Ptr(true),
		DisplayCount:    3,
	})

	effortIdx, err := effortSelector.Ask(ctx)
	fmt.Println()
	if err != nil {
		return nil, err
	}
	if effortIdx == nil {
		return nil, fmt.Errorf("reasoning effort selection cancelled")
	}
	selectedEffort := effortChoices[*effortIdx].Value

	// Prompt for model selection — fetch available models dynamically
	modelChoices := []*uxlib.SelectChoice{
		{Value: "", Label: "Default model (recommended)"},
	}

	// Start client to list models
	if startErr := a.clientManager.Start(ctx); startErr == nil {
		models, modelsErr := a.clientManager.ListModels(ctx)
		if modelsErr == nil {
			for _, m := range models {
				label := m.Name
				if m.DefaultReasoningEffort != "" {
					label += fmt.Sprintf(" (%s)", m.DefaultReasoningEffort)
				}
				if m.Billing != nil {
					label += fmt.Sprintf(" (%.0fx)", m.Billing.Multiplier)
				}
				modelChoices = append(modelChoices, &uxlib.SelectChoice{
					Value: m.ID,
					Label: label,
				})
			}
		}
	}

	modelSelector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message:         "Select AI model",
		HelpMessage:     "Premium models may use more requests. You can change this later.",
		Choices:         modelChoices,
		SelectedIndex:   intPtr(0),
		DisplayNumbers:  uxlib.Ptr(true),
		EnableFiltering: uxlib.Ptr(true),
		DisplayCount:    min(len(modelChoices), 10),
	})

	modelIdx, err := modelSelector.Ask(ctx)
	fmt.Println()
	if err != nil {
		return nil, err
	}
	if modelIdx == nil {
		return nil, fmt.Errorf("model selection cancelled")
	}
	selectedModel := modelChoices[*modelIdx].Value

	// Save to config
	if err := azdConfig.Set("ai.agent.reasoningEffort", selectedEffort); err != nil {
		return nil, fmt.Errorf("failed to save reasoning effort: %w", err)
	}
	if selectedModel != "" {
		if err := azdConfig.Set("ai.agent.model", selectedModel); err != nil {
			return nil, fmt.Errorf("failed to save model: %w", err)
		}
	}
	if err := a.configManager.Save(azdConfig); err != nil {
		return nil, fmt.Errorf("failed to save config: %w", err)
	}

	return &InitResult{
		Model:           selectedModel,
		ReasoningEffort: selectedEffort,
		IsFirstRun:      true,
	}, nil
}

// SelectSession shows a UX picker with previous sessions for the current directory.
// Returns the selected session metadata, or nil if user chose "new session".
func (a *CopilotAgent) SelectSession(ctx context.Context) (*SessionMetadata, error) {
	cwd, _ := os.Getwd()
	sessions, err := a.ListSessions(ctx, cwd)
	if err != nil || len(sessions) == 0 {
		return nil, nil
	}

	choices := make([]*uxlib.SelectChoice, 0, len(sessions)+1)
	choices = append(choices, &uxlib.SelectChoice{
		Value: "__new__",
		Label: "Start a new session",
	})

	for _, s := range sessions {
		timeStr := formatSessionTime(s.ModifiedTime)
		summary := ""
		if s.Summary != nil && *s.Summary != "" {
			summary = strings.Join(strings.Fields(*s.Summary), " ")
		}
		prefix := fmt.Sprintf("Resume (%s)", timeStr)
		if summary != "" {
			maxSummary := 120 - len(prefix) - 3
			if maxSummary > 10 {
				if len(summary) > maxSummary {
					summary = summary[:maxSummary-3] + "..."
				}
				prefix += " — " + summary
			}
		}
		choices = append(choices, &uxlib.SelectChoice{
			Value: s.SessionID,
			Label: prefix,
		})
	}

	selector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message:         "Previous sessions found",
		Choices:         choices,
		EnableFiltering: uxlib.Ptr(true),
		DisplayNumbers:  uxlib.Ptr(true),
		DisplayCount:    min(len(choices), 6),
	})

	idx, err := selector.Ask(ctx)
	fmt.Println()
	if err != nil {
		return nil, nil
	}
	if idx == nil || *idx == 0 {
		return nil, nil // new session
	}

	selected := sessions[*idx-1] // offset by 1 for "new session" choice
	return &selected, nil
}

// ListSessions returns previous sessions for the given working directory.
func (a *CopilotAgent) ListSessions(ctx context.Context, cwd string) ([]SessionMetadata, error) {
	if err := a.clientManager.Start(ctx); err != nil {
		return nil, err
	}

	return a.clientManager.Client().ListSessions(ctx, &copilot.SessionListFilter{
		Cwd: cwd,
	})
}

// SendMessage sends a prompt to the agent and returns the result.
// Creates a new session or resumes one if WithSessionID is provided.
func (a *CopilotAgent) SendMessage(ctx context.Context, prompt string, opts ...SendOption) (*AgentResult, error) {
	options := &sendOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// Ensure session exists
	if err := a.ensureSession(ctx, options.sessionID); err != nil {
		return nil, err
	}

	// Create display for this message turn
	display := NewAgentDisplay(a.console)
	a.display = display
	displayCtx, displayCancel := context.WithCancel(ctx)

	cleanup, err := display.Start(displayCtx)
	if err != nil {
		displayCancel()
		return nil, err
	}

	var watcher watch.Watcher
	watcher, _ = watch.NewWatcher(ctx)

	defer func() {
		displayCancel()
		time.Sleep(100 * time.Millisecond)
		cleanup()
		if watcher != nil {
			watcher.PrintChangedFiles(ctx)
		}
	}()

	// Subscribe display to session events
	unsubscribe := a.session.On(display.HandleEvent)
	defer unsubscribe()

	log.Printf("[copilot] SendMessage: sending prompt (%d chars)...", len(prompt))

	// Determine mode
	mode := a.mode
	if mode == "" {
		mode = AgentModeInteractive
	}

	// Send prompt (non-blocking)
	_, err = a.session.Send(ctx, copilot.MessageOptions{
		Prompt: prompt,
		Mode:   string(mode),
	})
	if err != nil {
		return nil, fmt.Errorf("copilot agent error: %w", err)
	}

	// Wait for idle — display handles all UX rendering
	content, err := display.WaitForIdle(ctx)
	if err != nil {
		return nil, err
	}

	return &AgentResult{
		Content:   content,
		SessionID: a.sessionID,
		Usage:     display.GetUsageMetrics(),
	}, nil
}

// SendMessageWithRetry wraps SendMessage with interactive retry-on-error UX.
func (a *CopilotAgent) SendMessageWithRetry(ctx context.Context, prompt string, opts ...SendOption) (*AgentResult, error) {
	for {
		result, err := a.SendMessage(ctx, prompt, opts...)
		if err != nil {
			if result != nil && result.Content != "" {
				a.console.Message(ctx, output.WithMarkdown(result.Content))
			}

			if shouldRetry := a.handleErrorWithRetryPrompt(ctx, err); shouldRetry {
				continue
			}
			return nil, err
		}

		return result, nil
	}
}

// Stop terminates the agent and performs cleanup in reverse order.
func (a *CopilotAgent) Stop() error {
	for i := len(a.cleanupTasks) - 1; i >= 0; i-- {
		task := a.cleanupTasks[i]
		if err := task.fn(); err != nil {
			log.Printf("failed to cleanup %s: %v", task.name, err)
		}
	}
	return nil
}

func (a *CopilotAgent) addCleanup(name string, fn func() error) {
	a.cleanupTasks = append(a.cleanupTasks, cleanupTask{name: name, fn: fn})
}

// ensureSession creates or resumes a Copilot session if one doesn't exist.
func (a *CopilotAgent) ensureSession(ctx context.Context, resumeSessionID string) error {
	if a.session != nil {
		return nil
	}

	// Ensure plugins
	a.ensurePlugins(ctx)

	// Start client
	if err := a.clientManager.Start(ctx); err != nil {
		return err
	}
	a.addCleanup("copilot-client", a.clientManager.Stop)

	// Load built-in MCP server configs
	builtInServers, err := loadBuiltInMCPServers()
	if err != nil {
		return err
	}

	// Build session config
	sessionConfig, err := a.sessionConfigBuilder.Build(ctx, builtInServers)
	if err != nil {
		return fmt.Errorf("failed to build session config: %w", err)
	}

	// Apply overrides
	if a.modelOverride != "" {
		sessionConfig.Model = a.modelOverride
	}
	if a.reasoningEffortOverride != "" {
		sessionConfig.ReasoningEffort = a.reasoningEffortOverride
	}

	log.Printf("[copilot] Session config (model=%q, mcpServers=%d)", sessionConfig.Model, len(sessionConfig.MCPServers))

	if resumeSessionID != "" {
		// Resume existing session
		resumeConfig := &copilot.ResumeSessionConfig{
			Model:               sessionConfig.Model,
			ReasoningEffort:     sessionConfig.ReasoningEffort,
			SystemMessage:       sessionConfig.SystemMessage,
			AvailableTools:      sessionConfig.AvailableTools,
			ExcludedTools:       sessionConfig.ExcludedTools,
			WorkingDirectory:    sessionConfig.WorkingDirectory,
			Streaming:           sessionConfig.Streaming,
			MCPServers:          sessionConfig.MCPServers,
			SkillDirectories:    sessionConfig.SkillDirectories,
			DisabledSkills:      sessionConfig.DisabledSkills,
			OnPermissionRequest: a.createPermissionHandler(),
			OnUserInputRequest:  a.createUserInputHandler(ctx),
			Hooks:               a.createHooks(ctx),
		}

		log.Printf("[copilot] Resuming session %s...", resumeSessionID)
		session, err := a.clientManager.Client().ResumeSession(ctx, resumeSessionID, resumeConfig)
		if err != nil {
			return fmt.Errorf("failed to resume session: %w", err)
		}
		a.session = session
		a.sessionID = resumeSessionID
		log.Println("[copilot] Session resumed")
	} else {
		// Create new session
		sessionConfig.OnPermissionRequest = a.createPermissionHandler()
		sessionConfig.OnUserInputRequest = a.createUserInputHandler(ctx)
		sessionConfig.Hooks = a.createHooks(ctx)

		log.Println("[copilot] Creating session...")
		session, err := a.clientManager.Client().CreateSession(ctx, sessionConfig)
		if err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}
		a.session = session
		a.sessionID = session.SessionID
		log.Printf("[copilot] Session created: %s", a.sessionID)
	}

	return nil
}

// createPermissionHandler builds the OnPermissionRequest handler.
// This handles CLI-level capability requests (e.g., "can I write files?", "can I run shell?").
// Without this handler, the SDK denies all tool categories and OnPreToolUse never fires.
// Currently approves all — fine-grained per-tool consent is enforced by OnPreToolUse.
// Can be expanded later with category-level checks if needed.
func (a *CopilotAgent) createPermissionHandler() copilot.PermissionHandlerFunc {
	return func(req copilot.PermissionRequest, inv copilot.PermissionInvocation) (
		copilot.PermissionRequestResult, error,
	) {
		log.Printf("[copilot] PermissionRequest: kind=%s — approved", req.Kind)
		return copilot.PermissionRequestResult{Kind: "approved"}, nil
	}
}

func (a *CopilotAgent) createUserInputHandler(ctx context.Context) copilot.UserInputHandler {
	return func(req copilot.UserInputRequest, inv copilot.UserInputInvocation) (
		copilot.UserInputResponse, error,
	) {
		question := stripMarkdown(req.Question)
		log.Printf("[copilot] UserInput: question=%q choices=%d", question, len(req.Choices))

		fmt.Println()

		if len(req.Choices) > 0 {
			choices := make([]*uxlib.SelectChoice, len(req.Choices))
			for i, c := range req.Choices {
				choices[i] = &uxlib.SelectChoice{Value: c, Label: stripMarkdown(c)}
			}

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
				EnableFiltering: uxlib.Ptr(true),
				DisplayNumbers:  uxlib.Ptr(true),
				DisplayCount:    min(len(choices), 10),
			})

			idx, err := selector.Ask(ctx)
			fmt.Println()
			if err != nil {
				return copilot.UserInputResponse{}, fmt.Errorf("cancelled: %w", err)
			}
			if idx == nil || *idx < 0 || *idx >= len(choices) {
				return copilot.UserInputResponse{}, fmt.Errorf("invalid selection")
			}

			selected := choices[*idx].Value
			if selected == freeformValue {
				prompt := uxlib.NewPrompt(&uxlib.PromptOptions{Message: question})
				answer, err := prompt.Ask(ctx)
				fmt.Println()
				if err != nil {
					return copilot.UserInputResponse{}, err
				}
				return copilot.UserInputResponse{Answer: answer, WasFreeform: true}, nil
			}

			return copilot.UserInputResponse{Answer: selected}, nil
		}

		prompt := uxlib.NewPrompt(&uxlib.PromptOptions{Message: question})
		answer, err := prompt.Ask(ctx)
		fmt.Println()
		if err != nil {
			return copilot.UserInputResponse{}, err
		}
		return copilot.UserInputResponse{Answer: answer, WasFreeform: true}, nil
	}
}

func (a *CopilotAgent) createHooks(ctx context.Context) *copilot.SessionHooks {
	return &copilot.SessionHooks{
		OnPreToolUse: func(input copilot.PreToolUseHookInput, inv copilot.HookInvocation) (
			*copilot.PreToolUseHookOutput, error,
		) {
			log.Printf("[copilot] PreToolUse: tool=%s", input.ToolName)

			consentReq := consent.ConsentRequest{
				ToolID:     fmt.Sprintf("copilot/%s", input.ToolName),
				ServerName: "copilot",
				Operation:  consent.OperationTypeTool,
			}

			decision, err := a.consentManager.CheckConsent(ctx, consentReq)
			if err != nil {
				log.Printf("[copilot] Consent check error for tool %s: %v, denying", input.ToolName, err)
				return &copilot.PreToolUseHookOutput{
					PermissionDecision:       "deny",
					PermissionDecisionReason: "consent check failed",
				}, nil
			}

			if decision.Allowed {
				return &copilot.PreToolUseHookOutput{PermissionDecision: "allow"}, nil
			}

			if decision.RequiresPrompt {
				checker := consent.NewConsentChecker(a.consentManager, "copilot")
				promptErr := checker.PromptAndGrantConsent(
					ctx, input.ToolName, input.ToolName, mcp.ToolAnnotation{},
				)
				if promptErr != nil {
					if errors.Is(promptErr, consent.ErrToolExecutionDenied) {
						return &copilot.PreToolUseHookOutput{
							PermissionDecision:       "deny",
							PermissionDecisionReason: "denied by user",
						}, nil
					}
					return &copilot.PreToolUseHookOutput{PermissionDecision: "deny"}, nil
				}
				return &copilot.PreToolUseHookOutput{PermissionDecision: "allow"}, nil
			}

			return &copilot.PreToolUseHookOutput{
				PermissionDecision:       "deny",
				PermissionDecisionReason: decision.Reason,
			}, nil
		},
		OnPostToolUse: func(input copilot.PostToolUseHookInput, inv copilot.HookInvocation) (
			*copilot.PostToolUseHookOutput, error,
		) {
			log.Printf("[copilot] PostToolUse: tool=%s", input.ToolName)
			return nil, nil
		},
		OnErrorOccurred: func(input copilot.ErrorOccurredHookInput, inv copilot.HookInvocation) (
			*copilot.ErrorOccurredHookOutput, error,
		) {
			log.Printf("[copilot] ErrorOccurred: error=%s recoverable=%v", input.Error, input.Recoverable)
			return nil, nil
		},
	}
}

func (a *CopilotAgent) handleErrorWithRetryPrompt(ctx context.Context, err error) bool {
	a.console.Message(ctx, "")
	a.console.Message(ctx, output.WithErrorFormat("Error occurred: %s", err.Error()))
	a.console.Message(ctx, "")

	retryPrompt := uxlib.NewConfirm(&uxlib.ConfirmOptions{
		Message:      "Want to try again?",
		DefaultValue: uxlib.Ptr(true),
	})

	shouldRetry, promptErr := retryPrompt.Ask(ctx)
	if promptErr != nil {
		return false
	}

	return shouldRetry != nil && *shouldRetry
}

func (a *CopilotAgent) ensurePlugins(ctx context.Context) {
	cliPath := a.clientManager.CLIPath()
	if cliPath == "" {
		cliPath = "copilot"
	}

	installed := getInstalledPlugins(ctx, cliPath)

	for _, plugin := range requiredPlugins {
		if installed[plugin.Name] {
			log.Printf("[copilot] Updating plugin: %s", plugin.Name)
			cmd := exec.CommandContext(ctx, cliPath, "plugin", "update", plugin.Name) //nolint:gosec
			out, err := cmd.CombinedOutput()
			if err != nil {
				log.Printf("[copilot] Plugin update warning: %v (%s)", err, string(out))
			}
		} else {
			log.Printf("[copilot] Installing plugin: %s", plugin.Source)
			cmd := exec.CommandContext(ctx, cliPath, "plugin", "install", plugin.Source) //nolint:gosec
			out, err := cmd.CombinedOutput()
			if err != nil {
				log.Printf("[copilot] Plugin install warning: %v (%s)", err, string(out))
			}
		}
	}
}

func getInstalledPlugins(ctx context.Context, cliPath string) map[string]bool {
	cmd := exec.CommandContext(ctx, cliPath, "plugin", "list")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}

	installed := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "•") || strings.HasPrefix(line, "\u2022") {
			name := strings.TrimPrefix(line, "•")
			name = strings.TrimPrefix(name, "\u2022")
			name = strings.TrimSpace(name)
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

func intPtr(v int) *int {
	return &v
}

func formatSessionTime(ts string) string {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, ts); err == nil {
			local := t.Local()
			now := time.Now()
			if local.Year() == now.Year() && local.YearDay() == now.YearDay() {
				return "Today " + local.Format("3:04 PM")
			}
			yesterday := now.AddDate(0, 0, -1)
			if local.Year() == yesterday.Year() && local.YearDay() == yesterday.YearDay() {
				return "Yesterday " + local.Format("3:04 PM")
			}
			return local.Format("Jan 2, 3:04 PM")
		}
	}
	if len(ts) > 19 {
		ts = ts[:19]
	}
	return ts
}

func stripMarkdown(s string) string {
	s = strings.TrimSpace(s)
	for _, marker := range []string{"***", "**", "*", "___", "__", "_"} {
		s = strings.ReplaceAll(s, marker, "")
	}
	s = strings.ReplaceAll(s, "`", "")
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
