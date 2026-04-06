// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
	uxlib "github.com/azure/azure-dev/cli/azd/pkg/ux"
)

// configKeyFirstRunCompleted is the user-config path that records
// the timestamp of a completed first-run experience.
const configKeyFirstRunCompleted = "tool.firstRunCompleted"

// envKeySkipFirstRun is the environment variable that, when set to
// "true", suppresses the first-run tool check entirely.
const envKeySkipFirstRun = "AZD_SKIP_FIRST_RUN"

// ToolFirstRunMiddleware presents a one-time welcome experience
// on the very first invocation of azd.  It detects the user's
// installed Azure development tools and optionally offers to
// install any missing recommended tools.
type ToolFirstRunMiddleware struct {
	configManager config.UserConfigManager
	console       input.Console
	manager       *tool.Manager
	options       *internal.GlobalCommandOptions
}

// NewToolFirstRunMiddleware creates a new [ToolFirstRunMiddleware].
func NewToolFirstRunMiddleware(
	configManager config.UserConfigManager,
	console input.Console,
	manager *tool.Manager,
	options *internal.GlobalCommandOptions,
) Middleware {
	return &ToolFirstRunMiddleware{
		configManager: configManager,
		console:       console,
		manager:       manager,
		options:       options,
	}
}

// Run executes the first-run experience if it has not been completed
// yet.  Regardless of whether the experience runs, the middleware
// always delegates to nextFn so the user's intended command is
// never blocked.
func (m *ToolFirstRunMiddleware) Run(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error) {
	// Skip for child actions (e.g. workflow steps).
	if IsChildAction(ctx) {
		return nextFn(ctx)
	}

	if m.shouldSkip(ctx) {
		return nextFn(ctx)
	}

	// Run the first-run experience.  Errors are logged but never
	// propagated — the user's command must always proceed.
	if err := m.runFirstRunExperience(ctx); err != nil {
		log.Printf("tool first-run experience failed: %v", err)
	}

	return nextFn(ctx)
}

// shouldSkip returns true when the first-run experience should be
// bypassed.  The reasons are checked in order of cost (cheapest
// first).
func (m *ToolFirstRunMiddleware) shouldSkip(ctx context.Context) bool {
	// 1. Env-var opt-out.
	if skip, _ := strconv.ParseBool(os.Getenv(envKeySkipFirstRun)); skip {
		return true
	}

	// 2. Non-interactive mode (--no-prompt).
	if m.options.NoPrompt {
		return true
	}

	// 3. CI/CD environment — never prompt in CI.
	if resource.IsRunningOnCI() {
		return true
	}

	// 4. Non-interactive terminal (piped stdin/stdout).
	if m.console.IsNoPromptMode() {
		return true
	}

	// 5. Already completed.
	cfg, err := m.configManager.Load()
	if err != nil {
		log.Printf("tool first-run: failed to load user config: %v", err)
		return true // err on the side of not blocking the user
	}

	if _, ok := cfg.Get(configKeyFirstRunCompleted); ok {
		return true
	}

	return false
}

// runFirstRunExperience drives the interactive welcome flow.
func (m *ToolFirstRunMiddleware) runFirstRunExperience(ctx context.Context) error {
	// ---------------------------------------------------------------
	// Welcome banner
	// ---------------------------------------------------------------
	m.console.Message(ctx, "")
	m.console.Message(ctx, output.WithBold("Welcome to Azure Developer CLI! 🚀"))
	m.console.Message(ctx, "")
	m.console.Message(ctx, "azd can help you set up your Azure development")
	m.console.Message(ctx, "environment with the right tools.")
	m.console.Message(ctx, "")

	// ---------------------------------------------------------------
	// Opt-in prompt
	// ---------------------------------------------------------------
	confirm := uxlib.NewConfirm(&uxlib.ConfirmOptions{
		Message:      "Would you like to check your Azure development tools?",
		DefaultValue: new(true),
	})
	runCheck, err := confirm.Ask(ctx)
	if err != nil {
		// Confirm can fail on interrupt/cancel — mark completed so
		// we don't pester the user again.
		m.markCompleted()
		if errors.Is(err, uxlib.ErrCancelled) {
			return nil
		}
		return fmt.Errorf("prompting for tool check: %w", err)
	}

	if runCheck == nil || !*runCheck {
		m.markCompleted()
		return nil
	}

	// ---------------------------------------------------------------
	// Tool detection
	// ---------------------------------------------------------------
	m.console.Message(ctx, "")

	var statuses []*tool.ToolStatus
	detectSpinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
		Text:        "Detecting tools...",
		ClearOnStop: true,
	})
	if err := detectSpinner.Run(ctx, func(ctx context.Context) error {
		var detectErr error
		statuses, detectErr = m.manager.DetectAll(ctx)
		return detectErr
	}); err != nil {
		m.markCompleted()
		return fmt.Errorf("detecting tools: %w", err)
	}

	// ---------------------------------------------------------------
	// Display results
	// ---------------------------------------------------------------
	m.console.Message(ctx, "")
	m.displayToolStatuses(ctx, statuses)

	// ---------------------------------------------------------------
	// Offer to install missing recommended tools
	// ---------------------------------------------------------------
	var missingRecommended []*tool.ToolStatus
	for _, s := range statuses {
		if !s.Installed && s.Tool != nil && s.Tool.Priority == tool.ToolPriorityRecommended {
			missingRecommended = append(missingRecommended, s)
		}
	}

	if len(missingRecommended) > 0 {
		if err := m.offerInstall(ctx, missingRecommended); err != nil {
			log.Printf("tool first-run: install offer failed: %v", err)
		}
	}

	m.markCompleted()
	return nil
}

// displayToolStatuses prints a summary line for each tool.
func (m *ToolFirstRunMiddleware) displayToolStatuses(
	ctx context.Context,
	statuses []*tool.ToolStatus,
) {
	for _, s := range statuses {
		if s.Tool == nil {
			continue
		}

		if s.Installed {
			version := s.InstalledVersion
			if version == "" {
				version = "installed"
			}
			m.console.Message(ctx,
				output.WithSuccessFormat("  ✓ %s (%s)", s.Tool.Name, version))
		} else {
			m.console.Message(ctx,
				output.WithWarningFormat("  ○ %s — not installed", s.Tool.Name))
		}
	}

	m.console.Message(ctx, "")
}

// offerInstall prompts the user to select missing recommended tools
// for installation and installs any selected tools.
func (m *ToolFirstRunMiddleware) offerInstall(
	ctx context.Context,
	missing []*tool.ToolStatus,
) error {
	choices := make([]*uxlib.MultiSelectChoice, len(missing))
	for i, s := range missing {
		choices[i] = &uxlib.MultiSelectChoice{
			Value:    s.Tool.Id,
			Label:    fmt.Sprintf("%s — %s", s.Tool.Name, s.Tool.Description),
			Selected: true, // pre-select all recommended
		}
	}

	multiSelect := uxlib.NewMultiSelect(&uxlib.MultiSelectOptions{
		Message: "Select recommended tools to install:",
		Choices: choices,
	})

	selected, err := multiSelect.Ask(ctx)
	if err != nil {
		if errors.Is(err, uxlib.ErrCancelled) {
			return nil
		}
		return fmt.Errorf("prompting for tool selection: %w", err)
	}

	if len(selected) == 0 {
		m.console.Message(ctx, output.WithGrayFormat(
			"No tools selected.  You can install them later with 'azd tool install'."))
		return nil
	}

	// Extract selected tool IDs.
	selectedIDs := make([]string, 0, len(selected))
	for _, choice := range selected {
		selectedIDs = append(selectedIDs, choice.Value)
	}

	// Install selected tools.
	m.console.Message(ctx, "")

	var results []*tool.InstallResult
	installSpinner := uxlib.NewSpinner(&uxlib.SpinnerOptions{
		Text:        "Installing tools...",
		ClearOnStop: true,
	})
	if err := installSpinner.Run(ctx, func(ctx context.Context) error {
		var installErr error
		results, installErr = m.manager.InstallTools(ctx, selectedIDs)
		return installErr
	}); err != nil {
		return fmt.Errorf("installing tools: %w", err)
	}

	// Display install results.
	m.console.Message(ctx, "")
	for _, r := range results {
		if r.Tool == nil {
			continue
		}

		if r.Success {
			version := r.InstalledVersion
			if version == "" {
				version = "ok"
			}
			m.console.Message(ctx,
				output.WithSuccessFormat("  ✓ %s installed (%s)", r.Tool.Name, version))
		} else {
			errMsg := "unknown error"
			if r.Error != nil {
				errMsg = r.Error.Error()
			}
			m.console.Message(ctx,
				output.WithWarningFormat("  ✗ %s — %s", r.Tool.Name, errMsg))
		}
	}

	m.console.Message(ctx, "")
	return nil
}

// markCompleted persists a timestamp in the user config so the
// first-run experience is not shown again.
func (m *ToolFirstRunMiddleware) markCompleted() {
	cfg, err := m.configManager.Load()
	if err != nil {
		log.Printf("tool first-run: failed to load config for marking complete: %v", err)
		return
	}

	if err := cfg.Set(configKeyFirstRunCompleted, time.Now().Format(time.RFC3339)); err != nil {
		log.Printf("tool first-run: failed to set config key: %v", err)
		return
	}

	if err := m.configManager.Save(cfg); err != nil {
		log.Printf("tool first-run: failed to save config: %v", err)
	}
}
