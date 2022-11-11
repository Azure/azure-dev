package ext

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bash"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
)

type HookType string

const (
	HookTypePre  HookType = "pre"
	HookTypePost HookType = "post"
)

type CommandHooks struct {
	commandRunner exec.CommandRunner
	cwd           string
	interactive   bool
	scripts       map[string]*project.ScriptConfig
	env           *environment.Environment
}

func NewCommandHooks(
	cwd string,
	commandRunner exec.CommandRunner,
	interactive bool,
	scripts map[string]*project.ScriptConfig,
	env *environment.Environment,
) *CommandHooks {
	return &CommandHooks{
		commandRunner: commandRunner,
		cwd:           cwd,
		interactive:   interactive,
		scripts:       scripts,
		env:           env,
	}
}

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

func (h *CommandHooks) getScriptsForHook(prefix HookType, commandName string) []*project.ScriptConfig {
	// Convert things like `azd config list` => 'configlist`
	commandName = strings.TrimPrefix(commandName, "azd")
	commandName = strings.TrimSpace(commandName)
	commandName = strings.ReplaceAll(commandName, " ", "")

	matchingScripts := []*project.ScriptConfig{}
	for scriptName, scriptConfig := range h.scripts {
		if strings.Contains(scriptName, string(prefix)) && strings.Contains(scriptName, commandName) {
			matchingScripts = append(matchingScripts, scriptConfig)
		}
	}

	return matchingScripts
}

func (h *CommandHooks) execScript(ctx context.Context, scriptConfig *project.ScriptConfig) error {
	log.Printf("Executing script '%s'", scriptConfig.Path)

	envVars := []string{}
	for k, v := range h.env.Values {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	script, err := getScript(h.commandRunner, scriptConfig.Type, h.cwd, envVars)
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
	scriptType project.ScriptType,
	cwd string,
	envVars []string,
) (tools.Script, error) {
	switch scriptType {
	case project.ScriptTypeBash:
		return bash.NewBashScript(commandRunner, cwd, envVars), nil
	case project.ScriptTypePowershell:
		return powershell.NewPowershellScript(commandRunner, cwd, envVars), nil
	default:
		return nil, fmt.Errorf(
			"script type '%s' is not a valid option. Bash and powershell scripts are the support options",
			scriptType,
		)
	}
}
