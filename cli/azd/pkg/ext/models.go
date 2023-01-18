package ext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ShellType string
type ScriptLocation string

const (
	ShellTypeBash         ShellType      = "sh"
	ShellTypePowershell   ShellType      = "pwsh"
	ScriptTypeUnknown     ShellType      = ""
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
		"unable to determine script type. Ensure 'Shell' parameter is set in configuration options",
	)
	ErrRunRequired           error = errors.New("run is always required")
	ErrUnsupportedScriptType error = errors.New("script type is not valid. Only '.sh' and '.ps1' are supported")
)

// Generic action function that may return an error
type InvokeFn func() error

// The type of hooks. Supported values are 'pre' and 'post'
type HookType string

// Azd hook configuration
type HookConfig struct {
	// The location of the script hook (file path or inline)
	location ScriptLocation
	// When location is `path` a file path must be specified relative to the project or service
	path string
	// Stores a value whether or not this hook config has been previously validated
	validated bool
	// Stores the working directory set for this hook config
	cwd string
	// When location is `inline` a script must be defined inline
	script string

	// Internal name of the hook running for a given command
	Name string `yaml:",omitempty"`
	// The type of script hook (bash or powershell)
	Shell ShellType `yaml:"shell,omitempty"`
	// The inline script to execute or path to existing file
	Run string `yaml:"run,omitempty"`
	// When set to true will not halt command execution even when a script error occurs.
	ContinueOnError bool `yaml:"continueOnError,omitempty"`
	// When set to true will bind the stdin, stdout & stderr to the running console
	Interactive bool `yaml:"interactive,omitempty"`
	// When running on windows use this override config
	Windows *HookConfig `yaml:"windows,omitempty"`
	// When running on linux/macos use this override config
	Posix *HookConfig `yaml:"linux,omitempty"`
}

// Validates and normalizes the hook configuration
func (hc *HookConfig) validate() error {
	if hc.validated {
		return nil
	}

	if hc.Run == "" {
		return ErrRunRequired
	}

	hc.Run = strings.ReplaceAll(hc.Run, "/", string(os.PathSeparator))

	scriptPath := hc.Run
	if hc.cwd != "" {
		scriptPath = filepath.Join(hc.cwd, hc.Run)
	}

	stats, err := os.Stat(scriptPath)
	if err == nil && !stats.IsDir() {
		hc.location = ScriptLocationPath
		hc.path = hc.Run
	} else {
		hc.location = ScriptLocationInline
		hc.script = hc.Run
	}

	if hc.Shell == ScriptTypeUnknown && hc.path == "" {
		return ErrScriptTypeUnknown
	}

	if hc.location == ScriptLocationUnknown {
		if hc.path != "" {
			hc.location = ScriptLocationPath
		} else if hc.script != "" {
			hc.location = ScriptLocationInline
		}
	}

	if hc.location == ScriptLocationInline {
		tempScript, err := createTempScript(hc)
		if err != nil {
			return err
		}

		hc.path = tempScript
	}

	if hc.Shell == ScriptTypeUnknown {
		scriptType, err := inferScriptTypeFromFilePath(hc.path)
		if err != nil {
			return err
		}

		hc.Shell = scriptType
	}

	hc.validated = true

	return nil
}

func inferScriptTypeFromFilePath(path string) (ShellType, error) {
	fileExtension := filepath.Ext(path)
	switch fileExtension {
	case ".sh":
		return ShellTypeBash, nil
	case ".ps1":
		return ShellTypePowershell, nil
	default:
		return "", fmt.Errorf(
			"script with file extension '%s' is not valid. %w.",
			fileExtension,
			ErrUnsupportedScriptType,
		)
	}
}

func createTempScript(hookConfig *HookConfig) (string, error) {
	var ext string
	scriptHeader := []string{}

	switch hookConfig.Shell {
	case ShellTypeBash:
		ext = "sh"
		scriptHeader = []string{
			"#!/bin/sh",
		}
	case ShellTypePowershell:
		ext = "ps1"
	}

	directory, err := os.MkdirTemp(os.TempDir(), "azd-*")
	if err != nil {
		return "", fmt.Errorf("failed creating temp directory, %w", err)
	}

	// Write the temporary script file to .azure/hooks folder
	file, err := os.CreateTemp(directory, fmt.Sprintf("%s-*.%s", hookConfig.Name, ext))
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
	scriptBuilder.WriteString(hookConfig.script)

	// Temp generated files are cleaned up automatically after script execution has completed.
	_, err = file.WriteString(scriptBuilder.String())
	if err != nil {
		return "", fmt.Errorf("failed writing hook file, %w", err)
	}

	return file.Name(), nil
}
