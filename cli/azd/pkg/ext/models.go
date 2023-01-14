package ext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

type ScriptType string
type ScriptLocation string

const (
	ScriptTypeBash        ScriptType     = "bash"
	ScriptTypePowershell  ScriptType     = "powershell"
	ScriptTypeUnknown     ScriptType     = ""
	ScriptLocationInline  ScriptLocation = "inline"
	ScriptLocationPath    ScriptLocation = "path"
	ScriptLocationUnknown ScriptLocation = ""
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

// Generic action function that may return an error
type InvokeFn func() error

// The type of hooks. Supported values are 'pre' and 'post'
type HookType string

type ScriptConfig struct {
	validated bool

	// Internal name of the hook running for a given command
	Name string `yaml:",omitempty"`
	// The type of script hook (bash or powershell)
	Type ScriptType `yaml:"type,omitempty"`
	// The location of the script hook (file path or inline)
	Location ScriptLocation `yaml:"location,omitempty"`
	// When location is `path` a file path must be specified relative to the project or service
	Path string `yaml:"path,omitempty"`
	// When location is `inline` a script must be defined inline
	Script string `yaml:"script,omitempty"`
	// When set to true will not halt command execution even when a script error occurs.
	ContinueOnError bool `yaml:"continueOnError,omitempty"`
	// When set to true will bind the stdin, stdout & stderr to the running console
	Interactive bool `yaml:"interactive,omitempty"`
	// When running on windows use this override config
	Windows *ScriptConfig `yaml:"windows,omitempty"`
	// When running on linux/macos use this override config
	Posix *ScriptConfig `yaml:"linux,omitempty"`
}

// Validates and normalizes the script configuration
func (sc *ScriptConfig) validate() error {
	if sc.validated {
		return nil
	}

	if sc.Type == ScriptTypeUnknown && sc.Path == "" {
		return ErrScriptTypeUnknown
	}

	if sc.Location == ScriptLocationInline && sc.Script == "" {
		return ErrScriptRequired
	}

	if sc.Location == ScriptLocationPath && sc.Path == "" {
		return ErrPathRequired
	}

	if sc.Location == ScriptLocationUnknown {
		if sc.Path != "" {
			sc.Location = ScriptLocationPath
		} else if sc.Script != "" {
			sc.Location = ScriptLocationInline
		}
	}

	if sc.Location == ScriptLocationInline {
		tempScript, err := createTempScript(sc)
		if err != nil {
			return err
		}

		sc.Path = tempScript
	}

	if sc.Type == ScriptTypeUnknown {
		scriptType, err := inferScriptTypeFromFilePath(sc.Path)
		if err != nil {
			return err
		}

		sc.Type = scriptType
	}

	_, err := os.Stat(sc.Path)
	if err != nil {
		return fmt.Errorf("script at '%s' is invalid, %w", sc.Path, err)
	}

	sc.validated = true

	return nil
}

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
