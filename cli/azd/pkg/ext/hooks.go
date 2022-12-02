package ext

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bash"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
	"golang.org/x/exp/slices"
)

// Generic action function that may return an error
type InvokeFn func() error

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
	console       input.Console
	cwd           string
	scripts       map[string]*ScriptConfig
	envVars       []string
}

// NewCommandHooks creates a new instance of CommandHooks
func NewCommandHooks(
	commandRunner exec.CommandRunner,
	console input.Console,
	scripts map[string]*ScriptConfig,
	cwd string,
	envVars []string,
) *CommandHooks {
	return &CommandHooks{
		commandRunner: commandRunner,
		console:       console,
		cwd:           cwd,
		scripts:       scripts,
		envVars:       envVars,
	}
}

// Invokes an action run runs any registered pre or post script hooks for the specified command.
func (h *CommandHooks) Invoke(ctx context.Context, commands []string, actionFn InvokeFn) error {
	err := h.RunScripts(ctx, HookTypePre, commands)
	if err != nil {
		return fmt.Errorf("failing running pre command hooks: %w", err)
	}

	err = actionFn()
	if err != nil {
		return err
	}

	err = h.RunScripts(ctx, HookTypePost, commands)
	if err != nil {
		return fmt.Errorf("failing running pre command hooks: %w", err)
	}

	return nil
}

// Invokes any registered script hooks for the specified hook type and command.
func (h *CommandHooks) RunScripts(ctx context.Context, hookType HookType, commands []string) error {
	scripts := h.getScriptsForHook(hookType, commands)
	for _, scriptConfig := range scripts {
		err := h.execScript(ctx, scriptConfig)
		if err != nil {
			return err
		}
	}

	return nil
}

func (h *CommandHooks) getScriptsForHook(prefix HookType, commands []string) []*ScriptConfig {
	validHookNames := []string{}

	for _, commandName := range commands {
		// Convert things like `azd config list` => 'configlist`
		commandName = strings.TrimPrefix(commandName, "azd")
		commandName = strings.TrimSpace(commandName)
		commandName = strings.ReplaceAll(commandName, " ", "")

		validHookNames = append(validHookNames, strings.ToLower(string(prefix)+commandName))
	}

	matchingScripts := []*ScriptConfig{}
	for scriptName, scriptConfig := range h.scripts {
		index := slices.IndexFunc(validHookNames, func(hookName string) bool {
			return hookName == scriptName
		})

		if index > -1 {
			scriptConfig.Name = scriptName
			matchingScripts = append(matchingScripts, scriptConfig)
		}
	}

	return matchingScripts
}

func (h *CommandHooks) execScript(ctx context.Context, scriptConfig *ScriptConfig) error {
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
					"Executing %s command hook => %s",
					output.WithHighLightFormat(scriptConfig.Name),
					output.WithHighLightFormat(scriptConfig.Path),
				),
			),
		)
	}

	log.Printf("Executing script '%s'", scriptConfig.Path)
	_, err = script.Execute(ctx, scriptConfig.Path, scriptInteractive)
	if err != nil {
		execErr := fmt.Errorf("failed executing script '%s' : %w", scriptConfig.Path, err)

		// If an error occurred log the failure but continue
		if scriptConfig.ContinueOnError {
			h.console.Message(ctx, output.WithBold(output.WithWarningFormat("WARNING: %s", execErr.Error())))
			h.console.Message(
				ctx,
				output.WithWarningFormat("'%s' script has been configured to continue on error.", scriptConfig.Name),
			)
			log.Println(execErr.Error())
		} else {
			return execErr
		}
	}

	return nil
}

// Gets the script to execute based on the script configuration values
// For inline scripts this will also create a temporary script file to execute
func (h *CommandHooks) GetScript(scriptConfig *ScriptConfig) (tools.Script, error) {
	if scriptConfig.Location == "" {
		if scriptConfig.Path != "" {
			scriptConfig.Location = ScriptLocationPath
		} else if scriptConfig.Script != "" {
			scriptConfig.Location = ScriptLocationInline
		}
	}

	if scriptConfig.Location == ScriptLocationInline {
		tempScript, err := createTempScript(scriptConfig)
		if err != nil {
			return nil, err
		}

		scriptConfig.Path = tempScript
	}

	if scriptConfig.Type == "" {
		fileExtension := filepath.Ext(scriptConfig.Path)
		switch fileExtension {
		case ".sh":
			scriptConfig.Type = ScriptTypeBash
		case ".ps1":
			scriptConfig.Type = ScriptTypePowershell
		default:
			return nil, fmt.Errorf(
				"script with file extension '%s' is not valid. Only '.sh' and '.ps1' are supported",
				fileExtension,
			)
		}
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

func createTempScript(scriptConfig *ScriptConfig) (string, error) {
	scriptBytes := []byte(scriptConfig.Script)
	hash := sha256.Sum256(scriptBytes)

	var ext string
	scriptHeader := []string{}

	switch scriptConfig.Type {
	case ScriptTypeBash:
		ext = "sh"
		scriptHeader = []string{
			"#!/bin/bash",
			"set -euo pipefail",
		}
	case ScriptTypePowershell:
		ext = "ps1"
	}

	filename := fmt.Sprintf("%s.%s", base64.URLEncoding.
		WithPadding(base64.NoPadding).
		EncodeToString(hash[:]), ext)

	// Write the temporary script file to .azure/hooks folder
	filePath := filepath.Join(".azure", "hooks", filename)
	directory := filepath.Dir(filePath)
	_, err := os.Stat(directory)
	if err != nil {
		err := os.MkdirAll(directory, osutil.PermissionDirectory)
		if err != nil {
			return "", fmt.Errorf("failed creating command hooks directory, %w", err)
		}
	}

	scriptBuilder := strings.Builder{}
	for _, line := range scriptHeader {
		scriptBuilder.WriteString(fmt.Sprintf("%s\n", line))
	}
	scriptBuilder.WriteString("\n")
	scriptBuilder.WriteString("# Auto generated file from Azure Developer CLI\n")
	scriptBuilder.Write([]byte(scriptConfig.Script))

	// Temp generated files are cleaned up automatically after script execution has completed.
	err = os.WriteFile(filePath, []byte(scriptBuilder.String()), osutil.PermissionFile)
	if err != nil {
		return "", fmt.Errorf("failed creating command hook file, %w", err)
	}

	return filePath, nil
}
