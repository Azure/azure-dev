// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package middleware

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/resource"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tool"
)

// ToolUpdateCheckMiddleware periodically checks for tool updates in the
// background and displays cached update notifications before command
// execution.  Notifications are only shown when the console is
// interactive and the current command is not a tool-management
// subcommand.
type ToolUpdateCheckMiddleware struct {
	manager       *tool.Manager
	console       input.Console
	options       *Options
	globalOptions *internal.GlobalCommandOptions
}

// NewToolUpdateCheckMiddleware creates a new [ToolUpdateCheckMiddleware].
// All dependencies are resolved by the IoC container.
func NewToolUpdateCheckMiddleware(
	manager *tool.Manager,
	console input.Console,
	options *Options,
	globalOptions *internal.GlobalCommandOptions,
) Middleware {
	return &ToolUpdateCheckMiddleware{
		manager:       manager,
		console:       console,
		options:       options,
		globalOptions: globalOptions,
	}
}

// Run executes the tool update check middleware.  Before the command
// runs it displays any cached update notifications.  After the command
// completes it triggers a background update check when the configured
// check interval has elapsed.
func (m *ToolUpdateCheckMiddleware) Run(ctx context.Context, nextFn NextFn) (*actions.ActionResult, error) {
	// Skip all notification and background-check logic for child
	// actions (e.g. workflow steps invoked by a parent command).
	if !IsChildAction(ctx) {
		m.showNotificationIfNeeded(ctx)
	}

	// Execute the actual command.
	result, err := nextFn(ctx)

	// Trigger an asynchronous update check after the command
	// completes so that results are cached for the next run.
	if !IsChildAction(ctx) {
		m.triggerBackgroundCheckIfNeeded(ctx)
	}

	return result, err
}

// showNotificationIfNeeded displays a one-line update notification
// when cached results indicate that tool updates are available and
// the current console session is interactive.
func (m *ToolUpdateCheckMiddleware) showNotificationIfNeeded(ctx context.Context) {
	if m.shouldSkipNotification() {
		return
	}

	hasUpdates, count, err := m.manager.HasUpdatesAvailable(ctx)
	if err != nil {
		log.Printf("tool-update-check: error checking cached updates: %v", err)
		return
	}

	if !hasUpdates || count == 0 {
		return
	}

	m.console.Message(ctx, output.WithHighLightFormat(
		"ℹ Updates available for %d Azure tool(s). Run 'azd tool check' to see details.", count,
	))

	if markErr := m.manager.MarkUpdateNotificationShown(ctx); markErr != nil {
		log.Printf("tool-update-check: error marking notification shown: %v", markErr)
	}
}

// shouldSkipNotification returns true when update notifications should
// be suppressed for the current invocation.
func (m *ToolUpdateCheckMiddleware) shouldSkipNotification() bool {
	// Non-interactive mode — user opted out of prompts and banners.
	if m.globalOptions.NoPrompt {
		return true
	}

	// CI/CD environment — no notifications.
	if resource.IsRunningOnCI() {
		return true
	}

	// Machine-readable output (JSON, table, etc.) — keep stdout clean.
	if !m.console.IsUnformatted() {
		return true
	}

	// Non-interactive terminal (piped stdin/stdout).
	if m.console.IsNoPromptMode() {
		return true
	}

	// The "azd tool" family of commands manages its own update UX;
	// showing a banner there would be redundant and noisy.
	if m.isToolCommand() {
		return true
	}

	return false
}

// isToolCommand reports whether the current command is "azd tool" or
// one of its subcommands (e.g. "azd tool check", "azd tool install").
func (m *ToolUpdateCheckMiddleware) isToolCommand() bool {
	return strings.HasPrefix(m.options.CommandPath, "azd tool")
}

// triggerBackgroundCheckIfNeeded spawns a non-blocking goroutine that
// performs a full tool update check when the configured check interval
// has elapsed.  The goroutine uses [context.WithoutCancel] so that it
// survives command completion, and includes a recover guard to prevent
// panics from crashing the CLI.
func (m *ToolUpdateCheckMiddleware) triggerBackgroundCheckIfNeeded(ctx context.Context) {
	// Honour the same opt-out signal used by the first-run experience.
	if skip, _ := strconv.ParseBool(os.Getenv(envKeySkipFirstRun)); skip {
		return
	}

	// CI/CD environment — skip background checks.
	if resource.IsRunningOnCI() {
		return
	}

	if !m.manager.ShouldCheckForUpdates(ctx) {
		return
	}

	//nolint:gosec // G118 – intentional: goroutine outlives request; parent ctx is cancelled on return.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("tool-update-check: recovered from panic in background check: %v", r)
			}
		}()

		// Use a bounded timeout so the goroutine always terminates,
		// even if tool detection hangs.  This prevents goroutine leaks
		// that cause CI pipelines to time out.
		checkCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if _, err := m.manager.CheckForUpdates(checkCtx); err != nil {
			log.Printf("tool-update-check: background check failed: %v", err)
		}
	}()
}
