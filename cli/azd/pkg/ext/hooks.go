package ext

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bash"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
)

// Generic action function that may return an error
type ActionFn func() error

// The type of command hooks. Supported values are 'pre' and 'post'
type HookType string

const (
	// Executes pre-command hooks
	HookTypePre HookType = "pre"
	// Execute post-command hooks
	HookTypePost HookType = "post"
)

// CommandHooks enable support to invoke integration scripts before & after commands
// Scripts can be invoked at the project or service level or
type CommandHooks struct {
	commandRunner exec.CommandRunner
	cwd           string
	interactive   bool
	scripts       map[string]*ScriptConfig
	envVars       []string
}

// NewCommandHooks creates a new instance of CommandHooks
func NewCommandHooks(
	commandRunner exec.CommandRunner,
	scripts map[string]*ScriptConfig,
	cwd string,
	envVars []string,
	interactive bool,
) *CommandHooks {
	return &CommandHooks{
		commandRunner: commandRunner,
		cwd:           cwd,
		interactive:   interactive,
		scripts:       scripts,
		envVars:       envVars,
	}
}

// Invokes an action run runs any registered pre or post script hooks for the specified command.
func (h *CommandHooks) InvokeAction(ctx context.Context, commandName string, actionFn ActionFn) error {
	err := h.RunScripts(ctx, HookTypePre, commandName)
	if err != nil {
		return fmt.Errorf("failing running pre command hooks: %w", err)
	}

	err = actionFn()
	if err != nil {
		return err
	}

	err = h.RunScripts(ctx, HookTypePost, commandName)
	if err != nil {
		return fmt.Errorf("failing running pre command hooks: %w", err)
	}

	return nil
}

// / Invokes any registered script hooks for the specified hook type and command.
func (h *CommandHooks) RunScripts(ctx context.Context, hookType HookType, commandName string) error {
	scripts := h.getScriptsForHook(hookType, commandName)
	for _, scriptConfig := range scripts {
		err := h.execScript(ctx, scriptConfig)
		if err != nil {
			return err
		}
	}

	return nil
}

func (h *CommandHooks) getScriptsForHook(prefix HookType, commandName string) []*ScriptConfig {
	// Convert things like `azd config list` => 'configlist`
	commandName = strings.TrimPrefix(commandName, "azd")
	commandName = strings.TrimSpace(commandName)
	commandName = strings.ReplaceAll(commandName, " ", "")

	matchingScripts := []*ScriptConfig{}
	for scriptName, scriptConfig := range h.scripts {
		if strings.Contains(scriptName, string(prefix)) && strings.Contains(scriptName, commandName) {
			matchingScripts = append(matchingScripts, scriptConfig)
		}
	}

	return matchingScripts
}

func (h *CommandHooks) execScript(ctx context.Context, scriptConfig *ScriptConfig) error {
	log.Printf("Executing script '%s'", scriptConfig.Path)

	script, err := getScript(h.commandRunner, scriptConfig, h.cwd, h.envVars)
	if err != nil {
		return err
	}

	_, err = script.Execute(ctx, scriptConfig.Path, h.interactive)
	if err != nil {
		return fmt.Errorf("failed executing script '%s' : %w", scriptConfig.Path, err)
	}

	return nil
}

func getScript(
	commandRunner exec.CommandRunner,
	scriptConfig *ScriptConfig,
	cwd string,
	envVars []string,
) (tools.Script, error) {
	if scriptConfig.Location == "" {
		scriptConfig.Location = ScriptLocationPath
	}

	if scriptConfig.Location == ScriptLocationInline {
		return nil, errors.New("inline script support is coming soon.")
	}

	if scriptConfig.Type == "" {
		fileExtension := filepath.Ext(scriptConfig.Path)
		switch fileExtension {
		case ".sh":
			scriptConfig.Type = ScriptTypeBash
		case ".ps1":
			scriptConfig.Type = ScriptTypePowershell
		default:
			return nil, fmt.Errorf("script with file extension '%s' is not valid. Only '.sh' and '.ps1' are supported", fileExtension)
		}
	}

	switch scriptConfig.Type {
	case ScriptTypeBash:
		return bash.NewBashScript(commandRunner, cwd, envVars), nil
	case ScriptTypePowershell:
		return powershell.NewPowershellScript(commandRunner, cwd, envVars), nil
	default:
		return nil, fmt.Errorf(
			"script type '%s' is not a valid option. Only Bash and powershell scripts are supported",
			scriptConfig.Type,
		)
	}
}
