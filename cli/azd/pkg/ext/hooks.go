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
	console       input.Console
	cwd           string
	interactive   bool
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
	interactive bool,
) *CommandHooks {
	return &CommandHooks{
		commandRunner: commandRunner,
		console:       console,
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
			scriptConfig.Name = fmt.Sprintf("%s-%s", prefix, commandName)
			matchingScripts = append(matchingScripts, scriptConfig)
		}
	}

	return matchingScripts
}

func (h *CommandHooks) execScript(ctx context.Context, scriptConfig *ScriptConfig) error {
	log.Printf("Executing script '%s'", scriptConfig.Path)

	// Delete any temporary inline scripts after execution
	defer func() {
		if scriptConfig.Location == ScriptLocationInline {
			os.Remove(scriptConfig.Path)
		}
	}()

	script, err := getScript(h.commandRunner, scriptConfig, h.cwd, h.envVars)
	if err != nil {
		return err
	}

	// When running in an interactive terminal broadcast a message to the dev to remind them that custom hooks are running.
	if h.interactive {
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
