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

	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
)

type HookFilterPredicateFn func(scriptName string, hookConfig *HookConfig) bool

// HooksManagerOptions configures a new [HooksManager].
type HooksManagerOptions struct {
	Cwd        string // Working directory for resolving relative hook paths
	ProjectDir string // Project root (azure.yaml location) for security boundary
}

// HooksManager enables support to invoke lifecycle hooks before & after
// commands. Hooks can be invoked at the project or service level.
type HooksManager struct {
	cwd           string
	projectDir    string
	commandRunner exec.CommandRunner
}

// NewHooksManager creates a new [HooksManager] instance.
// When [HooksManagerOptions.Cwd] is empty defaults to current shell
// working directory.
// [HooksManagerOptions.ProjectDir] is the project root directory
// (where azure.yaml lives), used as the security boundary for path
// containment. When empty, Cwd is used as the boundary.
func NewHooksManager(
	options HooksManagerOptions,
	commandRunner exec.CommandRunner,
) *HooksManager {
	cwd := options.Cwd
	if cwd == "" {
		osWd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		cwd = osWd
	}

	projectDir := options.ProjectDir
	if projectDir == "" {
		projectDir = cwd
	}

	return &HooksManager{
		cwd:           cwd,
		projectDir:    projectDir,
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
			hook.inputCwd = h.cwd
			hook.projectDir = h.projectDir

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
	URL        string
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
			if hookConfig.inputCwd == "" {
				hookConfig.inputCwd = h.cwd
			}
			if hookConfig.projectDir == "" {
				hookConfig.projectDir = h.projectDir
			}

			// Only perform shell detection for warning purposes, not full validation
			if !hookConfig.validated && hookConfig.Run != "" {
				// Check if it's an inline script (no file exists).
				// Mirror the Dir-aware resolution from parseRunTarget
				// so dir+run combinations are correctly detected as
				// file-based hooks.
				relativeCheckPath := strings.ReplaceAll(
					hookConfig.Run, "/", string(os.PathSeparator),
				)
				fullCheckPath := relativeCheckPath
				if hookConfig.inputCwd != "" {
					if filepath.IsAbs(relativeCheckPath) {
						fullCheckPath = relativeCheckPath
					} else if hookConfig.Dir != "" {
						dir := hookConfig.Dir
						if !filepath.IsAbs(dir) {
							dir = filepath.Join(
								hookConfig.inputCwd, dir,
							)
						}
						candidate := filepath.Join(
							dir, relativeCheckPath,
						)
						info, sErr := os.Stat(candidate)
						if sErr == nil && !info.IsDir() {
							fullCheckPath = candidate
						} else {
							fullCheckPath = filepath.Join(
								hookConfig.inputCwd,
								relativeCheckPath,
							)
						}
					} else {
						fullCheckPath = filepath.Join(
							hookConfig.inputCwd,
							relativeCheckPath,
						)
					}
				}

				_, err := os.Stat(fullCheckPath)
				isInlineScript := err != nil

				// If no kind/shell and it's an inline script, set
				// OS default Kind for warning purposes.
				if hookConfig.Shell == "" &&
					hookConfig.Kind == language.HookKindUnknown &&
					isInlineScript {
					hookConfig.Kind = defaultKindForOS()
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

	// Check runtime availability for hooks that require external runtimes.
	langWarnings := h.validateRuntimes(ctx, allHooks)
	result.Warnings = append(result.Warnings, langWarnings...)

	return result
}

// validateRuntimes inspects all hook configurations and verifies that
// required runtimes are installed. It returns warnings for each missing
// runtime, following the same pattern used for the PowerShell 7
// validation above.
//
// Currently only Python hooks are validated (Phase 1). JavaScript,
// TypeScript, and DotNet hooks are deferred to later phases.
func (h *HooksManager) validateRuntimes(
	ctx context.Context,
	allHooks map[string][]*HookConfig,
) []HookWarning {
	var warnings []HookWarning

	// Collect unique non-shell hook kinds required across all hooks.
	// Track the first hook name per kind for actionable messages.
	requiredLangs := map[language.HookKind]string{}

	for hookName, hookConfigs := range allHooks {
		for _, hookConfig := range hookConfigs {
			if hookConfig == nil {
				continue
			}

			// Apply OS-specific override if present.
			cfg := hookConfig
			if runtime.GOOS == "windows" && cfg.Windows != nil {
				cfg = cfg.Windows
			} else if (runtime.GOOS == "linux" ||
				runtime.GOOS == "darwin") && cfg.Posix != nil {
				cfg = cfg.Posix
			}

			if cfg.inputCwd == "" {
				cfg.inputCwd = h.cwd
			}
			if cfg.projectDir == "" {
				cfg.projectDir = h.projectDir
			}

			// Set the hook name so that any temp scripts
			// created by validate() use the correct name
			// pattern (e.g. azd-predeploy-*.sh).
			if cfg.Name == "" {
				cfg.Name = hookName
			}

			// Run validate to resolve the Kind field from
			// file extension / explicit config.
			if err := cfg.validate(); err != nil {
				// Validation errors are surfaced by GetAll /
				// GetByParams; skip the hook here.
				continue
			}

			// Non-shell hooks need runtime validation
			// (e.g. Python must be installed). Bash and
			// PowerShell hooks are validated separately above.
			if !cfg.Kind.IsShell() {
				if _, seen := requiredLangs[cfg.Kind]; !seen {
					requiredLangs[cfg.Kind] = hookName
				}
			}
		}
	}

	// Phase 1: validate Python runtime.
	if hookName, ok := requiredLangs[language.HookKindPython]; ok {
		pythonCli := python.NewCli(h.commandRunner)
		if err := pythonCli.CheckInstalled(ctx); err != nil {
			warnings = append(warnings, HookWarning{
				Message: fmt.Sprintf(
					"Python 3 is required to run hook '%s' "+
						"but is not installed",
					hookName,
				),
				Suggestion: fmt.Sprintf(
					"Install Python 3 from %s",
					output.WithHyperlink(
						pythonCli.InstallUrl(),
						"Python Downloads",
					),
				),
				URL: pythonCli.InstallUrl(),
			})
		}
	}

	// Phase 2: JS/TS — not yet validated.
	// Phase 4: DotNet — not yet validated.

	return warnings
}

// ValidateRuntimesErr is a convenience wrapper around ValidateHooks
// that returns an [errorhandler.ErrorWithSuggestion] when any required
// runtime is missing. Callers that need a hard early failure (e.g.
// before starting a long deployment) should use this instead of
// inspecting warnings manually.
func (h *HooksManager) ValidateRuntimesErr(
	ctx context.Context,
	allHooks map[string][]*HookConfig,
) error {
	warnings := h.validateRuntimes(ctx, allHooks)
	if len(warnings) == 0 {
		return nil
	}

	// Use the first warning for the primary error; additional
	// warnings are appended as links.
	first := warnings[0]
	links := make([]errorhandler.ErrorLink, 0, len(warnings))
	for _, w := range warnings {
		links = append(links, errorhandler.ErrorLink{
			URL:   w.URL,
			Title: w.Message,
		})
	}

	return &errorhandler.ErrorWithSuggestion{
		Err: fmt.Errorf(
			"missing required runtime: %s",
			first.Message,
		),
		Message:    first.Message,
		Suggestion: first.Suggestion,
		Links:      links,
	}
}
