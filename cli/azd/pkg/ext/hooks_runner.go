// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
)

// Hooks enable support to invoke integration scripts before & after commands
// Scripts can be invoked at the project or service level or
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

// NewHooks creates a new instance of CommandHooks
// When `cwd` is empty defaults to current shell working directory
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

// Invokes an action run runs any registered pre or post script hooks for the specified command.
func (h *HooksRunner) Invoke(ctx context.Context, commands []string, actionFn InvokeFn) error {
	err := h.RunHooks(ctx, HookTypePre, nil, commands...)
	if err != nil {
		return fmt.Errorf("failed running pre hooks: %w", err)
	}

	err = actionFn()
	if err != nil {
		return err
	}

	err = h.RunHooks(ctx, HookTypePost, nil, commands...)
	if err != nil {
		return fmt.Errorf("failed running post hooks: %w", err)
	}

	return nil
}

// Invokes any registered script hooks for the specified hook type and command.
func (h *HooksRunner) RunHooks(
	ctx context.Context,
	hookType HookType,
	options *tools.ExecOptions,
	commands ...string,
) error {
	hooks, err := h.hooksManager.GetByParams(h.hooks, hookType, commands...)
	if err != nil {
		return fmt.Errorf("failed running scripts for hooks '%s', %w", strings.Join(commands, ","), err)
	}

	for _, hookConfig := range hooks {
		if err := h.envManager.Reload(ctx, h.env); err != nil {
			return fmt.Errorf("reloading environment before running hook: %w", err)
		}

		err := h.execHook(ctx, hookConfig, options)
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
	ctx context.Context, hookConfig *HookConfig, options *tools.ExecOptions,
) error {
	if options == nil {
		options = &tools.ExecOptions{}
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
			return err
		}
	}

	// validate() resolves the hook's language, path, and shell type.
	if err := hookConfig.validate(); err != nil {
		return err
	}

	// Determine the boundary directory for project file discovery.
	boundaryDir := h.cwd
	if hookConfig.cwd != "" {
		boundaryDir = hookConfig.cwd
	}

	// Determine working directory from Dir (set explicitly or
	// auto-inferred from the run path by validate).
	cwd := h.cwd
	if hookConfig.Dir != "" {
		dir := hookConfig.Dir
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(boundaryDir, dir)
		}
		cwd = dir
	} else if hookConfig.path != "" && hookConfig.IsLanguageHook() {
		cwd = filepath.Dir(
			filepath.Join(boundaryDir, hookConfig.path),
		)
	}

	envVars := hookEnv.Environ()

	// Create executor (unified factory for ALL languages).
	pythonCli := python.NewCli(h.commandRunner)
	executor, err := language.GetExecutor(
		hookConfig.Language,
		h.commandRunner,
		pythonCli,
		boundaryDir,
		cwd,
		envVars,
	)
	if err != nil {
		if errors.Is(err, language.ErrUnsupportedLanguage) {
			return &errorhandler.ErrorWithSuggestion{
				Err: fmt.Errorf(
					"getting executor for hook '%s': %w",
					hookConfig.Name,
					err,
				),
				Message: fmt.Sprintf(
					"The '%s' language is not yet supported "+
						"for hook '%s'.",
					hookConfig.Language,
					hookConfig.Name,
				),
				Suggestion: "Currently only Python, Bash, and " +
					"PowerShell hooks are supported.",
			}
		}
		return fmt.Errorf(
			"getting executor for hook '%s': %w",
			hookConfig.Name, err,
		)
	}

	// Resolve script path. Language hooks need the full path so
	// Prepare can discover project files; shell hooks keep the
	// relative path because the executor's CWD handles resolution.
	scriptPath := hookConfig.path
	if hookConfig.cwd != "" && hookConfig.IsLanguageHook() {
		scriptPath = filepath.Join(hookConfig.cwd, hookConfig.path)
	}

	// Prepare (unified — venv/deps for Python, pwsh detection for PS, no-op for bash).
	log.Printf(
		"Preparing hook '%s' (%s)\n",
		hookConfig.Name, hookConfig.Language,
	)

	if err := executor.Prepare(ctx, scriptPath); err != nil {
		return &errorhandler.ErrorWithSuggestion{
			Err: fmt.Errorf(
				"preparing %s hook '%s': %w",
				hookConfig.Language,
				hookConfig.Name,
				err,
			),
			Message: fmt.Sprintf(
				"Failed to prepare %s hook '%s'.",
				hookConfig.Language,
				hookConfig.Name,
			),
			Suggestion: fmt.Sprintf(
				"Ensure the required runtime for '%s' is installed.",
				hookConfig.Language,
			),
		}
	}

	// Configure console/previewer.
	if h.configureExecOptions(ctx, hookConfig, options) {
		defer h.console.StopPreviewer(ctx, false)
	}

	// Execute (unified).
	log.Printf(
		"Executing hook '%s' (%s)\n",
		hookConfig.Name, scriptPath,
	)

	res, err := executor.Execute(ctx, scriptPath, *options)
	if err != nil {
		hookErr := h.handleHookError(
			ctx, hookConfig, res, scriptPath, err,
		)
		if hookErr != nil {
			return hookErr
		}
	}

	// Cleanup inline temp scripts.
	if hookConfig.location == ScriptLocationInline {
		defer os.Remove(hookConfig.path)
	}

	return nil
}

// configureExecOptions resolves interactive mode and sets up the
// console previewer for non-interactive hooks that have no custom
// stdout. This logic is shared by both shell and language hooks.
// Returns true when a previewer was started; the caller must defer
// [input.Console.StopPreviewer] in that case.
func (h *HooksRunner) configureExecOptions(
	ctx context.Context,
	hookConfig *HookConfig,
	options *tools.ExecOptions,
) bool {
	formatter := h.console.GetFormatter()
	consoleInteractive := (formatter == nil ||
		formatter.Kind() == output.NoneFormat)
	scriptInteractive := consoleInteractive && hookConfig.Interactive

	if options.Interactive == nil {
		options.Interactive = &scriptInteractive
	}

	// When the hook is not configured to run in interactive mode
	// and no stdout has been configured, show the hook execution
	// output within the console previewer pane.
	if !*options.Interactive && options.StdOut == nil {
		previewer := h.console.ShowPreviewer(
			ctx,
			&input.ShowPreviewerOptions{
				Prefix:       "  ",
				Title:        fmt.Sprintf("%s Hook Output", hookConfig.Name),
				MaxLineCount: 8,
			},
		)
		options.StdOut = previewer
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
