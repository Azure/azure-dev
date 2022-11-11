package middleware

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func UseCommandHooks() actions.MiddlewareFn {
	return func(ctx context.Context, options *actions.ActionOptions, next actions.NextFn) (*actions.ActionResult, error) {
		azdContext, err := azdcontext.NewAzdContext()
		if err != nil {
			log.Printf("failing creating AzdContext for command hooks, %s", err.Error())
			return nil, err
		}

		commandOptions := internal.GetCommandOptions(ctx)
		environmentName := &commandOptions.EnvironmentName
		if *environmentName == "" {
			*environmentName, err = azdContext.GetDefaultEnvironmentName()
			if err != nil {
				log.Printf("failing retrieving default environment name for command hooks. Command hooks will not run, %s\n", err.Error())
				return next(ctx)
			}
		}

		env, err := environment.GetEnvironment(azdContext, *environmentName)
		if err != nil {
			log.Printf("failing loading environment for command hooks. Command hooks will not run, %s\n", err.Error())
			return next(ctx)
		}

		projectConfig, err := project.LoadProjectConfig(azdContext.ProjectPath(), env)
		if err != nil {
			log.Printf("failing loading project for command hooks. Command hooks will not run, %s\n", err.Error())
			return next(ctx)
		}

		if projectConfig.Scripts == nil || len(projectConfig.Scripts) == 0 {
			log.Println("project does not contain any command hooks.")
			return next(ctx)
		}

		commandRunner := exec.GetCommandRunner(ctx)
		console := input.GetConsole(ctx)
		formatter := console.GetFormatter()
		interactive := formatter == nil || formatter.Kind() == output.NoneFormat

		hooks := newCommandHooks(azdContext.ProjectDirectory(), commandRunner, interactive, projectConfig, env)

		// Always run prescripts
		err = hooks.RunScripts(ctx, hookTypePre, options.Name)
		if err != nil {
			return nil, fmt.Errorf("failed running pre-command hooks: %w", err)
		}

		// Execute Action
		result, err := next(ctx)
		if err != nil {
			return result, err
		}

		// Only run post scripts on successful action
		err = hooks.RunScripts(ctx, hookTypePost, options.Name)
		if err != nil {
			return nil, fmt.Errorf("failed running post-command hooks: %w", err)
		}

		return result, err
	}
}

type hookType string

const (
	hookTypePre  hookType = "pre"
	hookTypePost hookType = "post"
)

type commandHooks struct {
	commandRunner exec.CommandRunner
	cwd           string
	interactive   bool
	projectConfig *project.ProjectConfig
	env           *environment.Environment
}

func newCommandHooks(cwd string, commandRunner exec.CommandRunner, interactive bool, projectConfig *project.ProjectConfig, env *environment.Environment) *commandHooks {
	return &commandHooks{
		commandRunner: commandRunner,
		cwd:           cwd,
		interactive:   interactive,
		projectConfig: projectConfig,
		env:           env,
	}
}

func (h *commandHooks) RunScripts(ctx context.Context, hookType hookType, commandName string) error {
	scripts := h.getScriptsForHook(hookType, commandName)
	for _, scriptConfig := range scripts {
		err := h.execScript(ctx, scriptConfig)
		if err != nil {
			return err
		}
	}

	return nil
}

func (h *commandHooks) getScriptsForHook(prefix hookType, commandName string) []*project.ScriptConfig {
	// Convert things like `azd config list` => 'configlist`
	commandName = strings.TrimPrefix(commandName, "azd")
	commandName = strings.TrimSpace(commandName)
	commandName = strings.ReplaceAll(commandName, " ", "")

	matchingScripts := []*project.ScriptConfig{}
	for scriptName, scriptConfig := range h.projectConfig.Scripts {
		if strings.Contains(scriptName, string(prefix)) && strings.Contains(scriptName, commandName) {
			matchingScripts = append(matchingScripts, scriptConfig)
		}
	}

	return matchingScripts
}

func (h *commandHooks) execScript(ctx context.Context, scriptConfig *project.ScriptConfig) error {
	log.Printf("Executing script '%s'", scriptConfig.Path)

	envVars := []string{}
	for k, v := range h.env.Values {
		envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
	}

	var runArgs exec.RunArgs

	if runtime.GOOS == "windows" {
		runArgs = exec.NewRunArgs("bash", scriptConfig.Path)
	} else {
		runArgs = exec.NewRunArgs("", scriptConfig.Path)
	}

	runArgs = runArgs.
		WithCwd(h.cwd).
		WithEnv(envVars).
		WithInteractive(h.interactive).
		WithShell(true)

	_, err := h.commandRunner.Run(ctx, runArgs)
	if err != nil {
		return fmt.Errorf("failed executing script '%s' : %w", scriptConfig.Path, err)
	}

	return nil
}
