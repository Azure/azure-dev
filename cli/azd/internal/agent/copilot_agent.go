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
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
	"github.com/azure/azure-dev/cli/azd/internal/cmd"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/watch"
)

// CopilotAgent is a self-contained agent backed by the GitHub Copilot SDK.
// It encapsulates initialization, session management, display, and usage tracking.
type CopilotAgent struct {
	// Dependencies
	clientManager        *agentcopilot.CopilotClientManager
	sessionConfigBuilder *agentcopilot.SessionConfigBuilder
	cli                  *agentcopilot.CopilotCLI
	consentManager       consent.ConsentManager
	console              input.Console
	configManager        config.UserConfigManager

	// Configuration overrides (from AgentOption)
	modelOverride           string
	reasoningEffortOverride string
	systemMessageOverride   string
	mode                    AgentMode
	debug                   bool
	headless                bool
	onSessionStarted        func(sessionID string)

	// Runtime state
	clientStarted          bool
	session                *copilot.Session
	sessionID              string
	activeCtx              atomic.Pointer[context.Context] // current SendMessage context for SDK callbacks
	display                *AgentDisplay                   // last display for usage metrics (interactive mode)
	mu                     sync.Mutex                      // guards cumulative metrics and file changes
	cumulativeUsage        UsageMetrics                    // cumulative metrics across multiple SendMessage calls
	accumulatedFileChanges watch.FileChanges               // accumulated file changes across all SendMessage calls
	messageCount           int                             // number of messages sent in current session
	consentApprovedCount   int                             // running count of tool calls approved
	consentDeniedCount     int                             // running count of tool calls denied

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
func (a *CopilotAgent) Initialize(ctx context.Context, opts ...InitOption) (result *InitResult, err error) {
	ctx, span := tracing.Start(ctx, events.CopilotInitializeEvent)
	defer func() {
		if result != nil {
			tracing.SetUsageAttributes(
				fields.CopilotInitIsFirstRun.Bool(result.IsFirstRun),
				fields.CopilotInitModel.String(result.Model),
				fields.CopilotInitReasoningEffort.String(result.ReasoningEffort),
			)
		}
		if err != nil {
			cmd.MapError(err, span)
		}
		span.End()
	}()

	options := &initOptions{}
	for _, opt := range opts {
		opt(options)
	}

	// Always ensure copilot is downloaded and user is authenticated first
	if err := a.ensureClientStarted(ctx); err != nil {
		return nil, err
	}

	if err := a.ensureAuthenticated(ctx); err != nil {
		return nil, err
	}

	// Load current config
	azdConfig, err := a.configManager.Load()
	if err != nil {
		return nil, err
	}

	existingModel, hasModel := azdConfig.GetString(agentcopilot.ConfigKeyModel)
	existingEffort, hasEffort := azdConfig.GetString(agentcopilot.ConfigKeyReasoningEffort)

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

	// Prompt for reasoning effort
	effortChoices := []*uxlib.SelectChoice{
		{Value: "low", Label: "Low — fastest, lowest cost"},
		{Value: "medium", Label: "Medium — balanced (recommended)"},
		{Value: "high", Label: "High — more thorough, higher cost and premium requests"},
	}

	effortSelector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message:         "Select reasoning effort level",
		HelpMessage:     "Higher reasoning uses more premium requests and may cost more. You can change this later.",
		Choices:         effortChoices,
		SelectedIndex:   new(1),
		DisplayNumbers:  uxlib.Ptr(true),
		EnableFiltering: new(false),
		DisplayCount:    3,
	})

	effortIdx, err := effortSelector.Ask(ctx)
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

	// Client already started — list models
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

	modelSelector := uxlib.NewSelect(&uxlib.SelectOptions{
		Message:         "Select AI model",
		HelpMessage:     "Premium models may use more requests. You can change this later.",
		Choices:         modelChoices,
		SelectedIndex:   new(0),
		DisplayNumbers:  uxlib.Ptr(true),
		EnableFiltering: uxlib.Ptr(true),
		DisplayCount:    min(len(modelChoices), 10),
	})

	modelIdx, err := modelSelector.Ask(ctx)
	if err != nil {
		return nil, err
	}
	if modelIdx == nil {
		return nil, fmt.Errorf("model selection cancelled")
	}
	selectedModel := modelChoices[*modelIdx].Value

	// Save to config
	if err := azdConfig.Set(agentcopilot.ConfigKeyReasoningEffort, selectedEffort); err != nil {
		return nil, fmt.Errorf("failed to save reasoning effort: %w", err)
	}
	if selectedModel != "" {
		if err := azdConfig.Set(agentcopilot.ConfigKeyModel, selectedModel); err != nil {
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
		timeStr := FormatSessionTime(s.ModifiedTime)
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
	a.console.Message(ctx, "")
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
	if err := a.ensureClientStarted(ctx); err != nil {
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

	// Update active context so SDK callbacks use this call's context
	a.activeCtx.Store(&ctx)

	// Ensure session exists
	if err := a.ensureSession(ctx, options.sessionID); err != nil {
		return nil, err
	}

	// Determine mode — headless defaults to autopilot
	mode := a.mode
	if mode == "" {
		if a.headless {
			mode = AgentModeAutopilot
		} else {
			mode = AgentModeInteractive
		}
	}

	log.Printf("[copilot] SendMessage: sending prompt (%d chars, headless=%v)...", len(prompt), a.headless)

	var result *AgentResult
	var err error

	if a.headless {
		result, err = a.sendMessageHeadless(ctx, prompt, mode)
	} else {
		result, err = a.sendMessageInteractive(ctx, prompt, mode)
	}

	if err != nil {
		return nil, err
	}

	// Increment message count only after successful send
	a.messageCount++

	return result, nil
}

// sendMessageInteractive sends a message with full interactive display and file watcher.
func (a *CopilotAgent) sendMessageInteractive(
	ctx context.Context, prompt string, mode AgentMode,
) (*AgentResult, error) {
	display := NewAgentDisplay(a.console)
	a.display = display
	displayCtx, displayCancel := context.WithCancel(ctx)

	cleanup, err := display.Start(displayCtx)
	if err != nil {
		displayCancel()
		return nil, err
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	watcher, watchErr := watch.NewWatcher(watchCtx)
	if watchErr != nil {
		log.Printf("[copilot] file watcher unavailable: %v", watchErr)
	}

	defer func() {
		watchCancel()
		displayCancel()
		time.Sleep(100 * time.Millisecond)
		cleanup()
	}()

	unsubscribe := a.session.On(display.HandleEvent)
	defer unsubscribe()

	_, err = a.session.Send(ctx, copilot.MessageOptions{
		Prompt: prompt,
		Mode:   string(mode),
	})
	if err != nil {
		return nil, fmt.Errorf("copilot agent error: %w", err)
	}

	if err := display.WaitForIdle(ctx); err != nil {
		return nil, err
	}

	turnUsage := display.GetUsageMetrics()
	a.mu.Lock()
	a.accumulateUsage(turnUsage)
	turnFileChanges := a.collectFileChanges(watcher)
	a.mu.Unlock()

	return &AgentResult{
		SessionID:   a.sessionID,
		Usage:       turnUsage,
		FileChanges: turnFileChanges,
	}, nil
}

// sendMessageHeadless sends a message silently using the HeadlessCollector.
func (a *CopilotAgent) sendMessageHeadless(
	ctx context.Context, prompt string, mode AgentMode,
) (*AgentResult, error) {
	collector := NewHeadlessCollector()

	watchCtx, watchCancel := context.WithCancel(ctx)
	watcher, watchErr := watch.NewWatcher(watchCtx)
	if watchErr != nil {
		log.Printf("[copilot] file watcher unavailable: %v", watchErr)
	}
	defer watchCancel()

	unsubscribe := a.session.On(collector.HandleEvent)
	defer unsubscribe()

	_, err := a.session.Send(ctx, copilot.MessageOptions{
		Prompt: prompt,
		Mode:   string(mode),
	})
	if err != nil {
		return nil, fmt.Errorf("copilot agent error: %w", err)
	}

	if err := collector.WaitForIdle(ctx); err != nil {
		return nil, err
	}

	turnUsage := collector.GetUsageMetrics()
	a.mu.Lock()
	a.accumulateUsage(turnUsage)
	turnFileChanges := a.collectFileChanges(watcher)
	a.mu.Unlock()

	return &AgentResult{
		SessionID:   a.sessionID,
		Usage:       turnUsage,
		FileChanges: turnFileChanges,
	}, nil
}

// accumulateUsage adds turn usage to the cumulative total.
func (a *CopilotAgent) accumulateUsage(turn UsageMetrics) {
	a.cumulativeUsage.InputTokens += turn.InputTokens
	a.cumulativeUsage.OutputTokens += turn.OutputTokens
	a.cumulativeUsage.DurationMS += turn.DurationMS
	a.cumulativeUsage.PremiumRequests += turn.PremiumRequests
	// These are per-request values, not cumulative — use latest
	if turn.Model != "" {
		a.cumulativeUsage.Model = turn.Model
	}
	if turn.BillingRate > 0 {
		a.cumulativeUsage.BillingRate = turn.BillingRate
	}
}

// GetMetrics returns cumulative session metrics (usage + file changes).
func (a *CopilotAgent) GetMetrics() AgentMetrics {
	a.mu.Lock()
	defer a.mu.Unlock()

	return AgentMetrics{
		Usage:       a.cumulativeUsage,
		FileChanges: a.accumulatedFileChanges,
	}
}

// GetMessages returns the session event log from the Copilot SDK.
// Returns an error if no session exists.
func (a *CopilotAgent) GetMessages(ctx context.Context) ([]SessionEvent, error) {
	if a.session == nil {
		return nil, fmt.Errorf("no active session")
	}

	return a.session.GetMessages(ctx)
}

// collectFileChanges stops the watcher, collects its changes, and appends them
// to the accumulated list. Returns the per-turn changes.
func (a *CopilotAgent) collectFileChanges(watcher watch.Watcher) watch.FileChanges {
	if watcher == nil {
		return nil
	}

	turnChanges := watcher.GetFileChanges()
	if len(turnChanges) > 0 {
		a.accumulatedFileChanges = append(a.accumulatedFileChanges, turnChanges...)
	}
	return turnChanges
}

// SendMessageWithRetry wraps SendMessage with interactive retry-on-error UX.
func (a *CopilotAgent) SendMessageWithRetry(ctx context.Context, prompt string, opts ...SendOption) (*AgentResult, error) {
	for {
		result, err := a.SendMessage(ctx, prompt, opts...)
		if err != nil {

			if shouldRetry := a.handleErrorWithRetryPrompt(ctx, err); shouldRetry {
				continue
			}
			return nil, err
		}

		return result, nil
	}
}

// Stop terminates the agent, cleans up the Copilot SDK client and runtime process.
// Cleanup tasks run in reverse registration order. Safe to call multiple times.
func (a *CopilotAgent) Stop() error {
	// Record all cumulative session metrics as usage attributes
	tracing.SetUsageAttributes(
		fields.CopilotMode.String(string(a.mode)),
		fields.CopilotSessionMessageCount.Int(a.messageCount),
		fields.CopilotMessageModel.String(a.cumulativeUsage.Model),
		fields.CopilotMessageInputTokens.Float64(a.cumulativeUsage.InputTokens),
		fields.CopilotMessageOutputTokens.Float64(a.cumulativeUsage.OutputTokens),
		fields.CopilotMessageBillingRate.Float64(a.cumulativeUsage.BillingRate),
		fields.CopilotMessagePremiumRequests.Float64(a.cumulativeUsage.PremiumRequests),
		fields.CopilotMessageDurationMs.Float64(a.cumulativeUsage.DurationMS),
		fields.CopilotConsentApprovedCount.Int(a.consentApprovedCount),
		fields.CopilotConsentDeniedCount.Int(a.consentDeniedCount),
	)
	if a.sessionID != "" {
		tracing.SetUsageAttributes(fields.CopilotSessionId.String(a.sessionID))
	}

	tasks := a.cleanupTasks
	a.cleanupTasks = nil

	var firstErr error
	for i := len(tasks) - 1; i >= 0; i-- {
		task := tasks[i]
		if err := task.fn(); err != nil {
			log.Printf("failed to cleanup %s: %v", task.name, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// SessionID returns the current session ID, or empty if no session exists.
func (a *CopilotAgent) SessionID() string {
	return a.sessionID
}

// activeContext returns the context from the currently executing SendMessage call.
// Used by SDK callbacks (permission, user input) that don't receive a context parameter.
func (a *CopilotAgent) activeContext() context.Context {
	if p := a.activeCtx.Load(); p != nil {
		return *p
	}
	return context.Background()
}

func (a *CopilotAgent) addCleanup(name string, fn func() error) {
	a.cleanupTasks = append(a.cleanupTasks, cleanupTask{name: name, fn: fn})
}

// ensureClientStarted starts the Copilot client if not already running.
// Idempotent — safe to call multiple times.
func (a *CopilotAgent) ensureClientStarted(ctx context.Context) error {
	if a.clientStarted {
		return nil
	}

	if err := a.clientManager.Start(ctx); err != nil {
		return err
	}
	a.addCleanup("copilot-client", a.clientManager.Stop)
	a.clientStarted = true
	return nil
}

// EnsureStarted starts the Copilot SDK client and verifies authentication.
// Call this eagerly to catch startup errors (missing binary, auth failures)
// before entering a chat loop. Idempotent — safe to call multiple times.
func (a *CopilotAgent) EnsureStarted(ctx context.Context) error {
	if err := a.ensureClientStarted(ctx); err != nil {
		return fmt.Errorf("starting copilot client: %w", err)
	}

	if err := a.ensureAuthenticated(ctx); err != nil {
		return fmt.Errorf("copilot authentication: %w", err)
	}

	return nil
}

// ensureSession creates or resumes a Copilot session if one doesn't exist.
// Uses context.WithoutCancel to prevent the SDK session from being torn down
// when a per-request context (e.g., gRPC) is cancelled between calls.
func (a *CopilotAgent) ensureSession(ctx context.Context, resumeSessionID string) (err error) {
	if a.session != nil {
		return nil
	}

	ctx, span := tracing.Start(ctx, events.CopilotSessionEvent)
	defer func() {
		// Log session ID even on failure (e.g., resume failure still has the attempted ID)
		sessionID := a.sessionID
		if sessionID == "" && resumeSessionID != "" {
			sessionID = resumeSessionID
		}
		if sessionID != "" {
			span.SetAttributes(fields.CopilotSessionId.String(sessionID))
		}
		if err != nil {
			cmd.MapError(err, span)
		}
		span.End()
	}()

	isResume := resumeSessionID != ""
	span.SetAttributes(fields.CopilotSessionIsNew.Bool(!isResume))

	// Detach from the caller's cancellation so the client and session
	// outlive individual requests (e.g., gRPC calls).
	sessionCtx := context.WithoutCancel(ctx)

	// Start client (extracts bundled CLI to cache if needed)
	if err := a.ensureClientStarted(sessionCtx); err != nil {
		return err
	}

	// Check authentication — prompt to sign in if needed
	if err := a.ensureAuthenticated(sessionCtx); err != nil {
		return err
	}

	// Ensure plugins — must run after Start() so the bundled CLI is extracted
	a.ensurePlugins(sessionCtx)

	// Load built-in MCP server configs
	builtInServers, err := loadBuiltInMCPServers()
	if err != nil {
		return err
	}

	// Build session config
	sessionConfig, err := a.sessionConfigBuilder.Build(sessionCtx, builtInServers)
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
	if a.systemMessageOverride != "" {
		sessionConfig.SystemMessage = &copilot.SystemMessageConfig{
			Mode:    "append",
			Content: a.systemMessageOverride,
		}
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
			OnUserInputRequest:  a.createUserInputHandler(sessionCtx),
			Hooks:               a.createHooks(sessionCtx),
		}

		log.Printf("[copilot] Resuming session %s...", resumeSessionID)
		session, err := a.clientManager.Client().ResumeSession(sessionCtx, resumeSessionID, resumeConfig)
		if err != nil {
			return fmt.Errorf("failed to resume session: %w", err)
		}
		a.session = session
		a.sessionID = resumeSessionID
		log.Println("[copilot] Session resumed")
	} else {
		// Create new session
		sessionConfig.OnPermissionRequest = a.createPermissionHandler()
		sessionConfig.OnUserInputRequest = a.createUserInputHandler(sessionCtx)
		sessionConfig.Hooks = a.createHooks(sessionCtx)

		log.Println("[copilot] Creating session...")
		session, err := a.clientManager.Client().CreateSession(sessionCtx, sessionConfig)
		if err != nil {
			return fmt.Errorf("creating copilot session: %w", err)
		}
		a.session = session
		a.sessionID = session.SessionID
		log.Printf("[copilot] Session created: %s", a.sessionID)
	}

	if a.onSessionStarted != nil {
		a.onSessionStarted(a.sessionID)
	}

	return nil
}

// createPermissionHandler builds the OnPermissionRequest handler.
// In headless mode, auto-approves all requests. Otherwise routes
// through the consent manager for unified access control.
func (a *CopilotAgent) createPermissionHandler() copilot.PermissionHandlerFunc {
	return func(req copilot.PermissionRequest, inv copilot.PermissionInvocation) (
		copilot.PermissionRequestResult, error,
	) {
		// In headless mode, auto-approve all permission requests
		if a.headless {
			log.Printf("[copilot] PermissionRequest (headless auto-approve): kind=%s", req.Kind)
			a.consentApprovedCount++
			return copilot.PermissionRequestResult{Kind: "approved"}, nil
		}

		server, tool, readOnly := permissionToConsentTarget(req)
		toolID := fmt.Sprintf("%s/%s", server, tool)

		log.Printf("[copilot] PermissionRequest: kind=%s target=%s", req.Kind, toolID)

		consentReq := consent.ConsentRequest{
			ToolID:     toolID,
			ServerName: server,
			Operation:  consent.OperationTypeTool,
			Annotations: mcp.ToolAnnotation{
				ReadOnlyHint: &readOnly,
			},
		}

		decision, err := a.consentManager.CheckConsent(a.activeContext(), consentReq)
		if err != nil {
			log.Printf("[copilot] Consent check error for %s: %v, denying", toolID, err)
			a.consentDeniedCount++
			return copilot.PermissionRequestResult{Kind: "denied-by-rules"}, nil
		}

		if decision.Allowed {
			a.consentApprovedCount++
			return copilot.PermissionRequestResult{Kind: "approved"}, nil
		}

		if decision.RequiresPrompt {
			// Pause the display to prevent ticker/spinner from
			// interfering with the interactive consent prompt
			if a.display != nil {
				a.display.Pause()
				defer a.display.Resume()
			}

			displayName := permissionDisplayName(req)
			checker := consent.NewConsentChecker(a.consentManager, server)
			description := buildPermissionDescription(req)

			promptErr := checker.PromptAndGrantConsent(
				a.activeContext(), tool, displayName, description, mcp.ToolAnnotation{ReadOnlyHint: &readOnly},
			)

			if promptErr != nil {
				if errors.Is(promptErr, consent.ErrToolExecutionSkipped) {
					// Skip — deny this tool but let the agent continue
					a.consentDeniedCount++
					return copilot.PermissionRequestResult{Kind: "denied-by-rules"}, nil
				}
				if errors.Is(promptErr, consent.ErrToolExecutionDenied) {
					// Deny — block and exit the interaction
					a.consentDeniedCount++
					return copilot.PermissionRequestResult{Kind: "denied-interactively-by-user"}, nil
				}
				log.Printf("[copilot] Consent grant error for %s: %v", toolID, promptErr)
				a.consentDeniedCount++
				return copilot.PermissionRequestResult{Kind: "denied-by-rules"}, nil
			}
			a.consentApprovedCount++
			return copilot.PermissionRequestResult{Kind: "approved"}, nil
		}

		a.consentDeniedCount++
		return copilot.PermissionRequestResult{Kind: "denied-by-rules"}, nil
	}
}

// permissionToConsentTarget maps a PermissionRequest to consent server/tool/readOnly.
func permissionToConsentTarget(req copilot.PermissionRequest) (server, tool string, readOnly bool) {
	switch req.Kind {
	case copilot.MCP:
		server = "copilot"
		if req.ServerName != nil {
			server = *req.ServerName
		}
		tool = "unknown"
		if req.ToolName != nil {
			tool = *req.ToolName
		}
		readOnly = req.ReadOnly != nil && *req.ReadOnly

	case copilot.KindShell:
		server = "copilot"
		tool = "shell"
		readOnly = false

	case copilot.Write:
		server = "copilot"
		tool = "write"
		readOnly = false

	case copilot.Read:
		server = "copilot"
		tool = "read"
		readOnly = true

	case copilot.URL:
		server = "copilot"
		tool = "url"
		readOnly = true

	case copilot.Memory:
		server = "copilot"
		tool = "memory"
		readOnly = false

	case copilot.CustomTool:
		server = "copilot"
		tool = "custom-tool"
		if req.ToolName != nil {
			tool = *req.ToolName
		}
		readOnly = false

	default:
		server = "copilot"
		tool = string(req.Kind)
		readOnly = false
	}

	return server, tool, readOnly
}

// buildPermissionDescription creates a rich description from a PermissionRequest
// for display in consent prompts.
func buildPermissionDescription(req copilot.PermissionRequest) string {
	var parts []string

	// Tool title/description
	if req.ToolTitle != nil && *req.ToolTitle != "" {
		parts = append(parts, *req.ToolTitle)
	} else if req.ToolDescription != nil && *req.ToolDescription != "" {
		parts = append(parts, *req.ToolDescription)
	}

	// Intention — what the agent wants to do
	if req.Intention != nil && *req.Intention != "" {
		parts = append(parts, fmt.Sprintf("Intent: %s", *req.Intention))
	}

	// Context-specific details
	switch req.Kind {
	case copilot.KindShell:
		if req.FullCommandText != nil && *req.FullCommandText != "" {
			parts = append(parts, fmt.Sprintf("Command: %s", *req.FullCommandText))
		}
	case copilot.Write:
		if req.FileName != nil && *req.FileName != "" {
			parts = append(parts, fmt.Sprintf("File: %s", *req.FileName))
		}
	case copilot.Read:
		if req.Path != nil && *req.Path != "" {
			parts = append(parts, fmt.Sprintf("Path: %s", *req.Path))
		}
	case copilot.URL:
		if req.URL != nil && *req.URL != "" {
			parts = append(parts, fmt.Sprintf("URL: %s", *req.URL))
		}
	case copilot.Memory:
		if req.Subject != nil && *req.Subject != "" {
			parts = append(parts, fmt.Sprintf("Subject: %s", *req.Subject))
		}
	}

	// Warning from the SDK
	if req.Warning != nil && *req.Warning != "" {
		parts = append(parts, fmt.Sprintf("⚠ %s", *req.Warning))
	}

	if len(parts) == 0 {
		return "No description available"
	}

	return strings.Join(parts, "\n")
}

// permissionDisplayName returns a user-friendly display name for the consent prompt.
func permissionDisplayName(req copilot.PermissionRequest) string {
	switch req.Kind {
	case copilot.KindShell:
		if req.FullCommandText != nil && *req.FullCommandText != "" {
			cmd := *req.FullCommandText
			if len(cmd) > 80 {
				cmd = cmd[:77] + "..."
			}
			return fmt.Sprintf("shell command: %s", cmd)
		}
		return "shell command"
	case copilot.Write:
		if req.FileName != nil && *req.FileName != "" {
			return fmt.Sprintf("write to %s", relativePath(*req.FileName))
		}
		return "file write"
	case copilot.Read:
		if req.Path != nil && *req.Path != "" {
			return fmt.Sprintf("read %s", relativePath(*req.Path))
		}
		return "file read"
	case copilot.URL:
		if req.URL != nil && *req.URL != "" {
			u := *req.URL
			if len(u) > 60 {
				u = u[:57] + "..."
			}
			return fmt.Sprintf("fetch %s", u)
		}
		return "URL access"
	case copilot.MCP:
		name := "tool"
		if req.ToolName != nil {
			name = *req.ToolName
		}
		return name
	default:
		return string(req.Kind)
	}
}

// relativePath converts an absolute path to relative from cwd if possible.
func relativePath(p string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}

func (a *CopilotAgent) createUserInputHandler(ctx context.Context) copilot.UserInputHandler {
	return func(req copilot.UserInputRequest, inv copilot.UserInputInvocation) (
		copilot.UserInputResponse, error,
	) {
		question := stripMarkdown(req.Question)
		log.Printf("[copilot] UserInput: question=%q choices=%d", question, len(req.Choices))

		if a.display != nil {
			a.display.Pause()
		}
		defer func() {
			if a.display != nil {
				a.display.Resume()
			}
		}()

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
				EnableFiltering: new(false),
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
				prompt := uxlib.NewPrompt(&uxlib.PromptOptions{
					Message:        question,
					IgnoreHintKeys: true,
				})
				answer, err := prompt.Ask(ctx)
				fmt.Println()
				if err != nil {
					return copilot.UserInputResponse{}, err
				}
				return copilot.UserInputResponse{Answer: answer, WasFreeform: true}, nil
			}

			return copilot.UserInputResponse{Answer: selected}, nil
		}

		// TODO: IgnoreHintKeys should not be needed — Prompt should auto-suppress
		// hint key handling when no HelpMessage is provided.
		prompt := uxlib.NewPrompt(&uxlib.PromptOptions{
			Message:        question,
			IgnoreHintKeys: true,
		})
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
			return &copilot.PreToolUseHookOutput{}, nil
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

// ensureAuthenticated checks GitHub Copilot auth status and prompts for login if needed.
func (a *CopilotAgent) ensureAuthenticated(ctx context.Context) error {
	authStatus, err := a.clientManager.GetAuthStatus(ctx)
	if err != nil {
		log.Printf("[copilot] Auth status check failed: %v", err)
		// Don't block — the SDK will handle auth errors during session creation
		return nil
	}

	if authStatus.IsAuthenticated {
		login := ""
		if authStatus.Login != nil {
			login = *authStatus.Login
		}
		log.Printf("[copilot] Authenticated as %s", login)
		return nil
	}

	// Not authenticated — prompt to sign in
	a.console.Message(ctx, "")
	a.console.Message(ctx, output.WithWarningFormat("Not authenticated with %s", agentcopilot.DisplayTitle))
	a.console.Message(ctx, "")

	confirm := uxlib.NewConfirm(&uxlib.ConfirmOptions{
		Message: fmt.Sprintf("Sign in to %s? (opens browser)", agentcopilot.DisplayTitle),
		HelpMessage: fmt.Sprintf(
			"%s requires GitHub authentication to access AI models and agent capabilities.",
			agentcopilot.DisplayTitle),
		DefaultValue: uxlib.Ptr(true),
	})

	shouldLogin, err := confirm.Ask(ctx)
	if err != nil {
		return fmt.Errorf("authentication prompt failed: %w", err)
	}

	if shouldLogin == nil || !*shouldLogin {
		return fmt.Errorf("%s authentication is required to continue", agentcopilot.DisplayTitle)
	}

	a.console.Message(ctx, "")
	if err := a.cli.Login(ctx); err != nil {
		return fmt.Errorf("%s sign-in failed: %w", agentcopilot.DisplayTitle, err)
	}

	// Verify auth succeeded
	authStatus, err = a.clientManager.GetAuthStatus(ctx)
	if err != nil || !authStatus.IsAuthenticated {
		return fmt.Errorf("%s authentication was not completed", agentcopilot.DisplayTitle)
	}

	a.console.Message(ctx, "")

	return nil
}

func (a *CopilotAgent) ensurePlugins(ctx context.Context) {
	// Skip plugin management in headless mode — plugins are managed externally
	if a.headless {
		return
	}

	// Plugin management requires "copilot" CLI in PATH (the npm-installed version).
	if _, err := exec.LookPath("copilot"); err != nil {
		log.Printf("[copilot] 'copilot' CLI not found in PATH — skipping plugin management")
		a.console.Message(ctx, output.WithWarningFormat(
			"The GitHub Copilot CLI is not installed. Some features may be limited.\n"+
				"Install it with: npm install -g @github/copilot"))
		return
	}

	installed, err := a.cli.ListPlugins(ctx)
	if err != nil {
		log.Printf("[copilot] Failed to list plugins: %v", err)
		return
	}

	for _, plugin := range requiredPlugins {
		if installed[plugin.Name] {
			log.Printf("[copilot] Plugin already installed: %s", plugin.Name)
			continue
		}

		shouldInstall, err := a.promptPluginInstall(ctx, plugin)
		if err != nil {
			log.Printf("[copilot] Plugin install prompt failed: %v", err)
			continue
		}

		if !shouldInstall {
			log.Printf("[copilot] User declined plugin install: %s", plugin.Name)
			continue
		}

		a.console.ShowSpinner(ctx, fmt.Sprintf("Installing %s plugin", plugin.Name), input.Step)
		if err := a.cli.InstallPlugin(ctx, plugin.Source); err != nil {
			a.console.StopSpinner(ctx, fmt.Sprintf("Installing %s plugin", plugin.Name), input.StepFailed)
			log.Printf("[copilot] Plugin install failed: %v", err)
		} else {
			a.console.StopSpinner(ctx, fmt.Sprintf("Installing %s plugin", plugin.Name), input.StepDone)
		}
	}
}

func (a *CopilotAgent) promptPluginInstall(ctx context.Context, plugin pluginSpec) (bool, error) {
	defaultYes := true

	a.console.Message(ctx, "")
	confirm := uxlib.NewConfirm(&uxlib.ConfirmOptions{
		Message: fmt.Sprintf(
			"%s works better with the %s plugin. Would you like to install it?",
			agentcopilot.DisplayTitle, plugin.Name),
		HelpMessage: "The Azure plugin provides:\n" +
			"• Azure MCP server that contains additional tools for Azure\n" +
			"• Skills that streamline and provide better results for creating, " +
			"validating, and deploying applications to Azure",
		DefaultValue: &defaultYes,
	})

	result, err := confirm.Ask(ctx)
	if err != nil {
		return false, err
	}

	if result == nil {
		return false, nil
	}

	return *result, nil
}

// FormatSessionTime formats a timestamp string into a human-friendly relative time display.
func FormatSessionTime(ts string) string {
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
			if after, ok := strings.CutPrefix(trimmed, prefix); ok {
				lines[i] = after
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}
