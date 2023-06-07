package ext

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/operations"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bash"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
)

// Hooks enable support to invoke integration scripts before & after commands
// Scripts can be invoked at the project or service level or
type HooksRunner struct {
	hooksManager     *HooksManager
	commandRunner    exec.CommandRunner
	operationManager operations.Manager
	console          input.Console
	cwd              string
	hooks            map[string]*HookConfig
	env              *environment.Environment
}

// NewHooks creates a new instance of CommandHooks
// When `cwd` is empty defaults to current shell working directory
func NewHooksRunner(
	hooksManager *HooksManager,
	commandRunner exec.CommandRunner,
	operationManager operations.Manager,
	console input.Console,
	cwd string,
	hooks map[string]*HookConfig,
	env *environment.Environment,
) *HooksRunner {
	if cwd == "" {
		osWd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		cwd = osWd
	}

	return &HooksRunner{
		hooksManager:     hooksManager,
		commandRunner:    commandRunner,
		operationManager: operationManager,
		console:          console,
		cwd:              cwd,
		hooks:            hooks,
		env:              env,
	}
}

// Invokes an action run runs any registered pre or post script hooks for the specified command.
func (h *HooksRunner) Invoke(ctx context.Context, commands []string, actionFn InvokeFn) error {
	err := h.RunHooks(ctx, HookTypePre, commands...)
	if err != nil {
		return fmt.Errorf("failed running pre hooks: %w", err)
	}

	err = actionFn()
	if err != nil {
		return err
	}

	err = h.RunHooks(ctx, HookTypePost, commands...)
	if err != nil {
		return fmt.Errorf("failed running post hooks: %w", err)
	}

	return nil
}

// Invokes any registered script hooks for the specified hook type and command.
func (h *HooksRunner) RunHooks(ctx context.Context, hookType HookType, commands ...string) error {
	hooks, err := h.hooksManager.GetByParams(h.hooks, hookType, commands...)
	if err != nil {
		return fmt.Errorf("failed running scripts for hooks '%s', %w", strings.Join(commands, ","), err)
	}

	for _, hookConfig := range hooks {
		if err := h.env.Reload(); err != nil {
			return fmt.Errorf("reloading environment before running hook: %w", err)
		}

		err := h.execHook(ctx, hookConfig)
		if err != nil {
			return err
		}

		if err := h.env.Reload(); err != nil {
			return fmt.Errorf("reloading environment after running hook: %w", err)
		}
	}

	return nil
}

// Gets the script to execute based on the hook configuration values
// For inline scripts this will also create a temporary script file to execute
func (h *HooksRunner) GetScript(hookConfig *HookConfig) (tools.Script, error) {
	if err := hookConfig.validate(); err != nil {
		return nil, err
	}

	switch hookConfig.Shell {
	case ShellTypeBash:
		return bash.NewBashScript(h.commandRunner, h.cwd, h.env.Environ()), nil
	case ShellTypePowershell:
		return powershell.NewPowershellScript(h.commandRunner, h.cwd, h.env.Environ()), nil
	default:
		return nil, fmt.Errorf(
			"shell type '%s' is not a valid option. Only 'sh' and 'pwsh' are supported",
			hookConfig.Shell,
		)
	}
}

func (h *HooksRunner) execHook(ctx context.Context, hookConfig *HookConfig) error {
	script, err := h.GetScript(hookConfig)
	if err != nil {
		return err
	}

	formatter := h.console.GetFormatter()
	consoleInteractive := formatter == nil || formatter.Kind() == output.NoneFormat
	scriptInteractive := consoleInteractive && hookConfig.Interactive

	operationMessage := fmt.Sprintf("Running %s hook", hookConfig.Name)
	return h.operationManager.Run(ctx, operationMessage, func(operation *operations.Operation) error {
		log.Printf("Executing script '%s'\n", hookConfig.path)
		res, err := script.Execute(ctx, hookConfig.path, scriptInteractive)
		if err != nil {
			execErr := fmt.Errorf(
				"'%s' hook failed with exit code: '%d', Path: '%s'. : %w",
				hookConfig.Name,
				res.ExitCode,
				hookConfig.path,
				err,
			)

			// If an error occurred log the failure but continue
			if hookConfig.ContinueOnError {
				log.Println(execErr.Error())
				operation.Warn(ctx, "Script failed! Execution will continue since ContinueOnError has been set to true.")
			} else {
				return execErr
			}
		}

		// Delete any temporary inline scripts after execution
		// Removing temp scripts only on success to support better debugging with failing scripts.
		if hookConfig.location == ScriptLocationInline {
			defer os.Remove(hookConfig.path)
		}

		return nil
	})
}
