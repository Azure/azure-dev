package ext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// Generic action function that may return an error
type InvokeFn func() error

// The type of hooks. Supported values are 'pre' and 'post'
type HookType string

const (
	// Executes pre hooks
	HookTypePre HookType = "pre"
	// Execute post hooks
	HookTypePost HookType = "post"
)

var (
	ErrScriptTypeUnknown error = errors.New(
		"unable to determine script type. Ensure 'Type' parameter is set in configuration options",
	)
	ErrScriptRequired        error = errors.New("script is required when location is set to 'Inline'")
	ErrPathRequired          error = errors.New("path is required when location is set to 'Path'")
	ErrUnsupportedScriptType error = errors.New("script type is not valid. Only '.sh' and '.ps1' are supported")
)

func inferScriptTypeFromFilePath(path string) (ScriptType, error) {
	fileExtension := filepath.Ext(path)
	switch fileExtension {
	case ".sh":
		return ScriptTypeBash, nil
	case ".ps1":
		return ScriptTypePowershell, nil
	default:
		return "", fmt.Errorf(
			"script with file extension '%s' is not valid. %w.",
			fileExtension,
			ErrUnsupportedScriptType,
		)
	}
}

func createTempScript(scriptConfig *ScriptConfig) (string, error) {
	var ext string
	scriptHeader := []string{}

	switch scriptConfig.Type {
	case ScriptTypeBash:
		ext = "sh"
		scriptHeader = []string{
			"#!/bin/sh",
		}
	case ScriptTypePowershell:
		ext = "ps1"
	}

	// Creates .azure/hooks directory if it doesn't already exist
	// In the future any scripts with names like "predeploy.sh" or similar would
	// automatically be invoked base on our hook naming convention
	directory := filepath.Join(".azure", "hooks")
	_, err := os.Stat(directory)
	if err != nil {
		err := os.MkdirAll(directory, osutil.PermissionDirectory)
		if err != nil {
			return "", fmt.Errorf("failed creating hooks directory, %w", err)
		}
	}

	// Write the temporary script file to .azure/hooks folder
	file, err := os.CreateTemp(directory, fmt.Sprintf("%s-*.%s", scriptConfig.Name, ext))
	if err != nil {
		return "", fmt.Errorf("failed creating hook file: %w", err)
	}

	defer file.Close()

	scriptBuilder := strings.Builder{}
	for _, line := range scriptHeader {
		scriptBuilder.WriteString(fmt.Sprintf("%s\n", line))
	}
	scriptBuilder.WriteString("\n")
	scriptBuilder.WriteString("# Auto generated file from Azure Developer CLI\n")
	scriptBuilder.WriteString(scriptConfig.Script)

	// Temp generated files are cleaned up automatically after script execution has completed.
	_, err = file.WriteString(scriptBuilder.String())
	if err != nil {
		return "", fmt.Errorf("failed writing hook file, %w", err)
	}

	return file.Name(), nil
}
