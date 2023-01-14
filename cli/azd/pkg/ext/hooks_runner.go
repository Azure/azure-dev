package ext

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bash"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
)

// Hooks enable support to invoke integration scripts before & after commands
// Scripts can be invoked at the project or service level or
type HooksRunner struct {
	hooksManager  *HooksManager
	commandRunner exec.CommandRunner
	console       input.Console
	cwd           string
	scripts       map[string]*ScriptConfig
	envVars       []string
}

// NewHooks creates a new instance of CommandHooks
// When `cwd` is empty defaults to current shell working directory
func NewHooksRunner(
	hooksManager *HooksManager,
	commandRunner exec.CommandRunner,
	console input.Console,
	cwd string,
	scripts map[string]*ScriptConfig,
	envVars []string,
) *HooksRunner {
	if cwd == "" {
		osWd, err := os.Getwd()
		if err != nil {
			panic(err)
		}

		cwd = osWd
	}

	return &HooksRunner{
		hooksManager:  hooksManager,
		commandRunner: commandRunner,
		console:       console,
		cwd:           cwd,
		scripts:       scripts,
		envVars:       envVars,
	}
}

// Invokes an action run runs any registered pre or post script hooks for the specified command.
func (h *HooksRunner) Invoke(ctx context.Context, commands []string, actionFn InvokeFn) error {
	err := h.RunHooks(ctx, HookTypePre, commands)
	if err != nil {
		return fmt.Errorf("failed running pre hooks: %w", err)
	}

	err = actionFn()
	if err != nil {
		return err
	}

	err = h.RunHooks(ctx, HookTypePost, commands)
	if err != nil {
		return fmt.Errorf("failed running post hooks: %w", err)
	}

	return nil
}

// Invokes any registered script hooks for the specified hook type and command.
func (h *HooksRunner) RunHooks(ctx context.Context, hookType HookType, commands []string) error {
	scripts, err := h.hooksManager.GetScriptConfigsForHook(h.scripts, hookType, commands...)
	if err != nil {
		return fmt.Errorf("failed running scripts for hooks '%s', %w", strings.Join(commands, ","), err)
	}

	for _, scriptConfig := range scripts {
		err := h.execScriptConfig(ctx, scriptConfig)
		if err != nil {
			return err
		}
	}

	return nil
}

// Gets the script to execute based on the script configuration values
// For inline scripts this will also create a temporary script file to execute
func (h *HooksRunner) GetScript(scriptConfig *ScriptConfig) (tools.Script, error) {
	if err := scriptConfig.validate(); err != nil {
		return nil, err
	}

	switch scriptConfig.Type {
	case ScriptTypeBash:
		return bash.NewBashScript(h.commandRunner, h.cwd, h.envVars), nil
	case ScriptTypePowershell:
		return powershell.NewPowershellScript(h.commandRunner, h.cwd, h.envVars), nil
	default:
		return nil, fmt.Errorf(
			"script type '%s' is not a valid option. Only Bash and powershell scripts are supported",
			scriptConfig.Type,
		)
	}
}

func (h *HooksRunner) execScriptConfig(ctx context.Context, scriptConfig *ScriptConfig) error {
	// Delete any temporary inline scripts after execution
	defer func() {
		if scriptConfig.Location == ScriptLocationInline {
			os.Remove(scriptConfig.Path)
		}
	}()

	script, err := h.GetScript(scriptConfig)
	if err != nil {
		return err
	}

	formatter := h.console.GetFormatter()
	consoleInteractive := formatter == nil || formatter.Kind() == output.NoneFormat
	scriptInteractive := consoleInteractive && scriptConfig.Interactive

	// When running in an interactive terminal broadcast a message to the dev to remind them that custom hooks are running.
	if consoleInteractive {
		h.console.Message(
			ctx,
			output.WithBold(
				fmt.Sprintf(
					"Executing %s hook => %s",
					output.WithHighLightFormat(scriptConfig.Name),
					output.WithHighLightFormat(scriptConfig.Path),
				),
			),
		)
	}

	log.Printf("Executing script '%s'\n", scriptConfig.Path)
	res, err := script.Execute(ctx, scriptConfig.Path, scriptInteractive)
	if err != nil {
		execErr := fmt.Errorf(
			"'%s' hook failed with exit code: '%d', Path: '%s'. : %w",
			scriptConfig.Name,
			res.ExitCode,
			scriptConfig.Path,
			err,
		)

		// If an error occurred log the failure but continue
		if scriptConfig.ContinueOnError {
			h.console.Message(ctx, output.WithBold(output.WithWarningFormat("WARNING: %s", execErr.Error())))
			h.console.Message(
				ctx,
				output.WithWarningFormat("Execution will continue since ContinueOnError has been set to true."),
			)
			log.Println(execErr.Error())
		} else {
			return execErr
		}
	}

	return nil
}
