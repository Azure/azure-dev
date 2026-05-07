// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/events"
	"github.com/azure/azure-dev/cli/azd/internal/tracing/fields"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"go.opentelemetry.io/otel/codes"
)

// HooksRunner enables support to invoke lifecycle hooks before & after
// commands. Hooks can be invoked at the project or service level.
type HooksRunner struct {
	hooksManager   *HooksManager
	commandRunner  exec.CommandRunner
	console        input.Console
	cwd            string
	hooks          map[string][]*HookConfig
	env            *environment.Environment
	envManager     environment.Manager
	serviceLocator ioc.ServiceLocator
}

// NewHooksRunner creates a new instance of HooksRunner.
// When `cwd` is empty defaults to current shell working directory.
func NewHooksRunner(
	hooksManager *HooksManager,
	commandRunner exec.CommandRunner,
	envManager environment.Manager,
	console input.Console,
	cwd string,
	hooks map[string][]*HookConfig,
	env *environment.Environment,
	serviceLocator ioc.ServiceLocator,
) *HooksRunner {
	if cwd == "" {
		osWd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		cwd = osWd
	}

	return &HooksRunner{
		hooksManager:   hooksManager,
		commandRunner:  commandRunner,
		envManager:     envManager,
		console:        console,
		cwd:            cwd,
		hooks:          hooks,
		env:            env,
		serviceLocator: serviceLocator,
	}
}

// Invoke runs any registered pre and post script hooks around the specified action.
// The hookType parameter identifies the hook scope for telemetry (project, layer, or service).
func (h *HooksRunner) Invoke(
	ctx context.Context, commands []string, hookType string, actionFn InvokeFn,
) error {
	err := h.RunHooks(ctx, HookTypePre, hookType, nil, commands...)
	if err != nil {
		return fmt.Errorf("failed running pre hooks: %w", err)
	}

	err = actionFn()
	if err != nil {
		return err
	}

	err = h.RunHooks(ctx, HookTypePost, hookType, nil, commands...)
	if err != nil {
		return fmt.Errorf("failed running post hooks: %w", err)
	}

	return nil
}

// RunHooks invokes any registered script hooks for the specified hook type and command.
// The hookType parameter identifies the hook scope for telemetry (project, layer, or service).
func (h *HooksRunner) RunHooks(
	ctx context.Context,
	ht HookType,
	hookType string,
	options *tools.ExecutionContext,
	commands ...string,
) error {
	hooks, err := h.hooksManager.GetByParams(h.hooks, ht, commands...)
	if err != nil {
		return fmt.Errorf("failed running scripts for hooks '%s', %w", strings.Join(commands, ","), err)
	}

	for _, hookConfig := range hooks {
		if err := h.envManager.Reload(ctx, h.env); err != nil {
			return fmt.Errorf("reloading environment before running hook: %w", err)
		}

		err := h.execHook(ctx, hookConfig, hookType, options)
		if err != nil {
			return err
		}

		if err := h.envManager.Reload(ctx, h.env); err != nil {
			return fmt.Errorf("reloading environment after running hook: %w", err)
		}
	}

	return nil
}

func (h *HooksRunner) execHook(
	ctx context.Context, hookConfig *HookConfig, hookType string, options *tools.ExecutionContext,
) (hookErr error) {
	// Create a child span for this hook execution so each hook
	// gets its own correlated set of telemetry attributes.
	ctx, span := tracing.Start(ctx, events.HooksExecEvent)
	statusCode := ""
	defer func() {
		if hookErr != nil {
			if statusCode == "" {
				statusCode = "hook.failed"
			}
			span.SetStatus(codes.Error, statusCode)
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}()

	if options == nil {
		options = &tools.ExecutionContext{}
	}

	hookEnv := environment.NewWithValues("temp", h.env.Dotenv())
	if len(hookConfig.Secrets) > 0 {
		err := h.serviceLocator.Invoke(func(keyvaultService keyvault.KeyVaultService) error {
			for key, value := range hookConfig.Secrets {
				setValue := value
				if valueFromEnv, exists := h.env.LookupEnv(value); exists {
					if keyvault.IsAzureKeyVaultSecret(valueFromEnv) {
						secretValue, err := keyvaultService.SecretFromAkvs(ctx, valueFromEnv)
						if err != nil {
							return err
						}
						valueFromEnv = secretValue
					}
					setValue = valueFromEnv
				}
				hookEnv.DotenvSet(key, setValue)
			}
			return nil
		})
		if err != nil {
			statusCode = "hook.secrets_failed"
			return err
		}
	}

	// Set name and type on span — known before validation.
	span.SetAttributes(
		fields.HooksNameKey.String(hookConfig.Name),
		fields.HooksTypeKey.String(hookType),
	)

	// validate() resolves the hook's kind, path, shell type,
	// and computes resolvedDir / resolvedScriptPath.
	if err := hookConfig.validate(); err != nil {
		statusCode = "hook.validation_failed"
		return err
	}

	span.SetAttributes(
		fields.HooksKindKey.String(string(hookConfig.Kind)))

	// Use pre-resolved paths from validate().
	cwd := hookConfig.resolvedDir
	if cwd == "" {
		cwd = h.cwd // fallback (shouldn't happen after validate)
	}

	boundaryDir := hookConfig.projectDir
	if boundaryDir == "" {
		boundaryDir = hookConfig.inputCwd
	}
	if boundaryDir == "" {
		boundaryDir = h.cwd
	}

	scriptPath := hookConfig.resolvedScriptPath

	envVars := hookEnv.Environ()

	// Build execution context.
	execCtx := tools.ExecutionContext{
		Cwd:          cwd,
		EnvVars:      envVars,
		BoundaryDir:  boundaryDir,
		InlineScript: hookConfig.inlineScript,
		HookName:     hookConfig.Name,
		Config:       hookConfig.Config,
	}

	// Merge caller-provided overrides (e.g. forced interactive from 'azd hooks run').
	if options.Interactive != nil {
		execCtx.Interactive = options.Interactive
	}
	if options.StdOut != nil {
		execCtx.StdOut = options.StdOut
	}

	// Resolve executor via IoC — hooks runner has NO knowledge of executor internals.
	var executor tools.HookExecutor
	if err := h.serviceLocator.ResolveNamed(string(hookConfig.Kind), &executor); err != nil {
		statusCode = "hook.executor_not_found"
		return &errorhandler.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"no executor for kind '%s': %w",
				hookConfig.Kind, err,
			),
			Message: fmt.Sprintf(
				"The '%s' kind is not supported for hook '%s'.",
				hookConfig.Kind,
				hookConfig.Name,
			),
			Suggestion: "Supported hook kinds: sh, pwsh, python, js, ts.",
			Links: []errorhandler.ErrorLink{
				{
					Title: "Hook documentation",
					URL:   "https://learn.microsoft.com/azure/developer/azure-developer-cli/azd-extensibility",
				},
			},
		}
	}

	// Cleanup temp resources created during Prepare (e.g. inline
	// script temp files). Deferred before Prepare so cleanup runs
	// even if Prepare fails partway through. Cleanup is safe to
	// call when Prepare was not called or created no resources.
	defer func() {
		if cErr := executor.Cleanup(ctx); cErr != nil {
			log.Printf("warning: cleanup failed for hook '%s': %v\n", hookConfig.Name, cErr)
		}
	}()

	// Prepare (unified — venv/deps for Python, pwsh detection for
	// PowerShell, inline temp file creation for Bash/PowerShell hooks).
	log.Printf(
		"Preparing hook '%s' (%s)\n",
		hookConfig.Name, hookConfig.Kind,
	)

	if err := executor.Prepare(ctx, scriptPath, execCtx); err != nil {
		statusCode = "hook.prepare_failed"
		return fmt.Errorf("preparing hook '%s': %w", hookConfig.Name, err)
	}

	// Configure console/previewer.
	if h.configureExecContext(ctx, hookConfig, &execCtx) {
		defer h.console.StopPreviewer(ctx, false)
	}

	// Execute (unified).
	log.Printf(
		"Executing hook '%s' (%s)\n",
		hookConfig.Name, scriptPath,
	)

	res, err := executor.Execute(ctx, scriptPath, execCtx)
	if err != nil {
		execErr := h.handleHookError(
			ctx, hookConfig, res, scriptPath, err,
		)
		if execErr != nil {
			statusCode = "hook.execution_failed"
			return execErr
		}
	}

	return nil
}

// configureExecContext resolves interactive mode and sets up the
// console previewer for non-interactive hooks that have no custom
// stdout. Returns true when a previewer was started; the caller must
// defer [input.Console.StopPreviewer] in that case.
func (h *HooksRunner) configureExecContext(
	ctx context.Context,
	hookConfig *HookConfig,
	execCtx *tools.ExecutionContext,
) bool {
	formatter := h.console.GetFormatter()
	consoleInteractive := (formatter == nil ||
		formatter.Kind() == output.NoneFormat)
	scriptInteractive := consoleInteractive && hookConfig.Interactive

	if execCtx.Interactive == nil {
		execCtx.Interactive = &scriptInteractive
	}

	// When the hook is not configured to run in interactive mode
	// and no stdout has been configured, show the hook execution
	// output within the console previewer pane.
	if !*execCtx.Interactive && execCtx.StdOut == nil {
		previewer := h.console.ShowPreviewer(
			ctx,
			&input.ShowPreviewerOptions{
				Prefix:       "  ",
				Title:        fmt.Sprintf("%s Hook Output", hookConfig.Name),
				MaxLineCount: 8,
			},
		)
		execCtx.StdOut = previewer
		return true
	}

	return false
}

// handleHookError wraps a hook execution error and either returns
// it or logs a warning when ContinueOnError is set.
func (h *HooksRunner) handleHookError(
	ctx context.Context,
	hookConfig *HookConfig,
	res exec.RunResult,
	scriptPath string,
	err error,
) error {
	execErr := fmt.Errorf(
		"'%s' hook failed with exit code: '%d', Path: '%s'. : %w",
		hookConfig.Name,
		res.ExitCode,
		scriptPath,
		err,
	)

	if hookConfig.ContinueOnError {
		h.console.Message(
			ctx,
			output.WithBold(
				"%s",
				output.WithWarningFormat("WARNING: %s", execErr.Error()),
			),
		)
		h.console.Message(
			ctx,
			output.WithWarningFormat(
				"Execution will continue since ContinueOnError has been set to true.",
			),
		)
		log.Println(execErr.Error())
		return nil
	}

	return execErr
}
