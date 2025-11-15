// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/pkg/exec"
	"github.com/azure/azure-dev/pkg/output"
)

type HookFilterPredicateFn func(scriptName string, hookConfig *HookConfig) bool

// Hooks enable support to invoke integration scripts before & after commands
// Scripts can be invoked at the project or service level or
type HooksManager struct {
	cwd           string
	commandRunner exec.CommandRunner
}

// NewHooks creates a new instance of CommandHooks
// When `cwd` is empty defaults to current shell working directory
func NewHooksManager(
	cwd string,
	commandRunner exec.CommandRunner,
) *HooksManager {
	if cwd == "" {
		osWd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		cwd = osWd
	}

	return &HooksManager{
		cwd:           cwd,
		commandRunner: commandRunner,
	}
}

// Gets an array of all hook configurations
// Will return an error if any configuration errors are found
func (h *HooksManager) GetAll(hooks map[string][]*HookConfig) ([]*HookConfig, error) {
	return h.filterConfigs(hooks, nil)
}

// Gets an array of hook configurations matching the specified hook type and commands
// Will return an error if any configuration errors are found
func (h *HooksManager) GetByParams(
	hooks map[string][]*HookConfig,
	prefix HookType,
	commands ...string,
) ([]*HookConfig, error) {
	validHookNames := map[string]struct{}{}

	for _, commandName := range commands {
		// Convert things like `azd env list` => 'envlist`
		commandName = strings.TrimPrefix(commandName, "azd")
		commandName = strings.TrimSpace(commandName)
		commandName = strings.ReplaceAll(commandName, " ", "")
		commandName = strings.ToLower(string(prefix) + commandName)
		validHookNames[commandName] = struct{}{}
	}

	predicate := func(scriptName string, hookConfig *HookConfig) bool {
		_, has := validHookNames[scriptName]
		return has
	}

	return h.filterConfigs(hooks, predicate)
}

// Filters the specified hook configurations based on the predicate
// Will return an error if any configuration errors are found
func (h *HooksManager) filterConfigs(
	hooksMap map[string][]*HookConfig,
	predicate HookFilterPredicateFn,
) ([]*HookConfig, error) {
	matchingHooks := []*HookConfig{}

	// Find explicitly configured hooks from azure.yaml
	for scriptName, hooks := range hooksMap {
		if hooks == nil {
			continue
		}

		for _, hook := range hooks {
			if hook == nil {
				log.Printf("hook configuration for '%s' is missing", scriptName)
				continue
			}

			if predicate != nil && !predicate(scriptName, hook) {
				continue
			}

			// If the hook config includes an OS specific configuration use that instead
			if runtime.GOOS == "windows" && hook.Windows != nil {
				hook = hook.Windows
			} else if (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && hook.Posix != nil {
				hook = hook.Posix
			}

			hook.Name = scriptName
			hook.cwd = h.cwd

			if err := hook.validate(); err != nil {
				return nil, fmt.Errorf("hook configuration for '%s' is invalid, %w", scriptName, err)
			}

			matchingHooks = append(matchingHooks, hook)
		}
	}

	return matchingHooks, nil
}

// HookValidationResult contains warnings found during hook validation
type HookValidationResult struct {
	Warnings []HookWarning
}

// HookWarning represents a validation warning for hooks
type HookWarning struct {
	Message    string
	Suggestion string
}

// ValidateHooks validates hook configurations and returns any warnings
func (h *HooksManager) ValidateHooks(ctx context.Context, allHooks map[string][]*HookConfig) *HookValidationResult {
	result := &HookValidationResult{
		Warnings: []HookWarning{},
	}

	hasPowerShellHooks := false
	hasDefaultShellHooks := false

	// Two-pass validation is required because:
	// 1. First pass: Set shell defaults and detect inline scripts for each hook configuration
	// 2. Second pass: Generate warnings only after all hooks have been processed and we know
	//    the complete state (e.g., whether ANY hook uses PowerShell or default shell)
	// We cannot merge these loops because warnings depend on global state across all hooks.

	// First pass: perform lightweight validation to set flags like usingDefaultShell
	// without creating temporary files (which full validation does)
	for _, hookConfigs := range allHooks {
		for _, hookConfig := range hookConfigs {
			// Set the working directory for validation
			if hookConfig.cwd == "" {
				hookConfig.cwd = h.cwd
			}

			// Only perform shell detection for warning purposes, not full validation
			if !hookConfig.validated && hookConfig.Run != "" {
				// Check if it's an inline script (no file exists)
				relativeCheckPath := strings.ReplaceAll(hookConfig.Run, "/", string(os.PathSeparator))
				fullCheckPath := relativeCheckPath
				if hookConfig.cwd != "" {
					fullCheckPath = filepath.Join(hookConfig.cwd, hookConfig.Run)
				}

				_, err := os.Stat(fullCheckPath)
				isInlineScript := err != nil // File doesn't exist, so it's inline

				// If shell is not specified and it's an inline script, set default for warning
				if hookConfig.Shell == ScriptTypeUnknown && isInlineScript {
					if runtime.GOOS == "windows" {
						hookConfig.Shell = ShellTypePowershell
					} else {
						hookConfig.Shell = ShellTypeBash
					}
					hookConfig.usingDefaultShell = true
				}
			}
		}
	}

	// Second pass: check all hooks for warning conditions using the state set in first pass
	for _, hookConfigs := range allHooks {
		for _, hookConfig := range hookConfigs {
			if hookConfig.IsPowerShellHook() {
				hasPowerShellHooks = true
			}
			if hookConfig.IsUsingDefaultShell() {
				hasDefaultShellHooks = true
			}
		}
	}

	// If we found PowerShell hooks, check if pwsh is available
	if hasPowerShellHooks {
		if err := h.commandRunner.ToolInPath("pwsh"); errors.Is(err, osexec.ErrNotFound) {
			var warningMessage string

			// Check if legacy powershell is available
			if powershellErr := h.commandRunner.ToolInPath("powershell"); !errors.Is(powershellErr, osexec.ErrNotFound) {
				//nolint:lll
				warningMessage = "PowerShell 7 (`pwsh`) commands found in project. Your computer only has PowerShell 5.1 (`powershell`) installed. azd will use `powershell` but errors may occur."
			} else {
				//nolint:lll
				warningMessage = "PowerShell 7 (`pwsh`) commands found in project. No PowerShell installation detected. PowerShell scripts will fail."
			}

			result.Warnings = append(result.Warnings, HookWarning{
				Message: warningMessage,
				Suggestion: fmt.Sprintf(
					"To resolve warning, install `pwsh` (%s)",
					output.WithHyperlink(
						"https://learn.microsoft.com/powershell/scripting/install/installing-powershell",
						"Install Instructions",
					),
				),
			})
		}
	}

	// If we found hooks using default shell, warn the user - only log
	if hasDefaultShellHooks {
		var warningMessage string
		var defaultShell string

		if runtime.GOOS == "windows" {
			defaultShell = "pwsh"
		} else {
			defaultShell = "sh"
		}

		warningMessage = fmt.Sprintf(
			"Hook configurations found without explicit shell specification. Using OS default shell '%s'. "+
				"For better reliability, consider specifying the shell explicitly in your hook configuration.\n"+
				"More about using hooks: %s",
			defaultShell,
			output.WithHyperlink("aka.ms/azd-hooks", "aka.ms/azd-hooks"),
		)

		log.Println(warningMessage)
	}

	return result
}
