// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
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

	// 3. Non-interactive terminal (piped stdin/stdout).
	if m.console.IsNoPromptMode() {
		return true
	}

	// 4. Already completed.
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
	runCheck, err := m.console.Confirm(ctx, input.ConsoleOptions{
		Message:      "Would you like to check your Azure development tools?",
		DefaultValue: true,
	})
	if err != nil {
		// Confirm can fail on interrupt/cancel — mark completed so
		// we don't pester the user again.
		m.markCompleted()
		return fmt.Errorf("prompting for tool check: %w", err)
	}

	if !runCheck {
		m.markCompleted()
		return nil
	}

	// ---------------------------------------------------------------
	// Tool detection
	// ---------------------------------------------------------------
	m.console.Message(ctx, "")
	m.console.ShowSpinner(ctx, "Detecting tools...", input.Step)

	statuses, err := m.manager.DetectAll(ctx)

	m.console.StopSpinner(ctx, "", input.StepDone)

	if err != nil {
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
	options := make([]string, 0, len(missing))
	toolIDs := make([]string, 0, len(missing))
	for _, s := range missing {
		options = append(options, fmt.Sprintf("%s — %s", s.Tool.Name, s.Tool.Description))
		toolIDs = append(toolIDs, s.Tool.Id)
	}

	selected, err := m.console.MultiSelect(ctx, input.ConsoleOptions{
		Message:      "Select recommended tools to install:",
		Options:      options,
		DefaultValue: options, // pre-select all by default
	})
	if err != nil {
		return fmt.Errorf("prompting for tool selection: %w", err)
	}

	if len(selected) == 0 {
		m.console.Message(ctx, output.WithGrayFormat(
			"No tools selected.  You can install them later with 'azd tool install'."))
		return nil
	}

	// Map selected display strings back to tool IDs.
	selectedIDs := make([]string, 0, len(selected))
	for _, sel := range selected {
		for i, opt := range options {
			if sel == opt {
				selectedIDs = append(selectedIDs, toolIDs[i])
				break
			}
		}
	}

	// Install selected tools.
	m.console.Message(ctx, "")
	m.console.ShowSpinner(ctx, "Installing tools...", input.Step)

	results, err := m.manager.InstallTools(ctx, selectedIDs)

	m.console.StopSpinner(ctx, "", input.StepDone)

	if err != nil {
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
