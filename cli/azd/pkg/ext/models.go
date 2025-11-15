// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/azure/azure-dev/pkg/osutil"
)

// The type of hooks. Supported values are 'pre' and 'post'
type HookType string
type HookPlatformType string
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
	HookTypePost        HookType         = "post"
	HookTypeNone        HookType         = ""
	HookPlatformWindows HookPlatformType = "windows"
	HookPlatformPosix   HookPlatformType = "posix"
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
	// Indicates if the shell was automatically detected based on OS (used for warnings)
	usingDefaultShell bool

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
	Posix *HookConfig `yaml:"posix,omitempty"`
	// Environment variables in this list are added to the hook script and if the value is a akvs:// reference
	// it will be resolved to the secret value
	Secrets map[string]string `yaml:"secrets,omitempty"`
}

// Validates and normalizes the hook configuration
func (hc *HookConfig) validate() error {
	if hc.validated {
		return nil
	}

	if hc.Run == "" {
		return ErrRunRequired
	}

	relativeCheckPath := strings.ReplaceAll(hc.Run, "/", string(os.PathSeparator))
	fullCheckPath := relativeCheckPath
	if hc.cwd != "" {
		fullCheckPath = filepath.Join(hc.cwd, hc.Run)
	}

	stats, err := os.Stat(fullCheckPath)
	if err == nil && !stats.IsDir() {
		hc.location = ScriptLocationPath
		hc.path = relativeCheckPath
	} else {
		hc.location = ScriptLocationInline
		hc.script = hc.Run
	}

	// If shell is not specified and it's an inline script, use OS default
	if hc.Shell == ScriptTypeUnknown && hc.path == "" {
		hc.Shell = getDefaultShellForOS()
		hc.usingDefaultShell = true
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

// IsPowerShellHook determines if a hook configuration uses PowerShell
func (hc *HookConfig) IsPowerShellHook() bool {
	// Check if shell is explicitly set to pwsh
	if hc.Shell == ShellTypePowershell {
		return true
	}

	// Check if shell is unknown but the hook file has .ps1 extension
	if hc.Shell == ScriptTypeUnknown && hc.Run != "" {
		// For file-based hooks, check the extension
		if strings.HasSuffix(strings.ToLower(hc.Run), ".ps1") {
			return true
		}
	}

	// Check OS-specific hook configurations
	if runtime.GOOS == "windows" && hc.Windows != nil {
		return hc.Windows.IsPowerShellHook()
	} else if (runtime.GOOS == "linux" || runtime.GOOS == "darwin") && hc.Posix != nil {
		return hc.Posix.IsPowerShellHook()
	}

	return false
}

// IsUsingDefaultShell returns true if the hook is using the OS default shell
// because no shell was explicitly configured
func (hc *HookConfig) IsUsingDefaultShell() bool {
	return hc.usingDefaultShell
}

func InferHookType(name string) (HookType, string) {
	// Validate name length so go doesn't PANIC for string slicing below
	if len(name) < 4 {
		return HookTypeNone, name
	} else if name[:3] == "pre" {
		return HookTypePre, name[3:]
	} else if name[:4] == "post" {
		return HookTypePost, name[4:]
	}

	return HookTypeNone, name
}

// getDefaultShellForOS returns the default shell type based on the operating system
func getDefaultShellForOS() ShellType {
	if runtime.GOOS == "windows" {
		return ShellTypePowershell
	}
	return ShellTypeBash
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
	scriptFooter := []string{}

	switch ShellType(strings.Split(string(hookConfig.Shell), " ")[0]) {
	case ShellTypeBash:
		ext = "sh"
		scriptHeader = []string{
			"#!/bin/sh",
			"set -e",
		}
	case ShellTypePowershell:
		ext = "ps1"
		scriptHeader = []string{
			"$ErrorActionPreference = 'Stop'",
		}
		scriptFooter = []string{
			"if ((Test-Path -LiteralPath variable:\\LASTEXITCODE)) { exit $LASTEXITCODE }",
		}
	}

	// Write the temporary script file to OS temp dir
	file, err := os.CreateTemp(os.TempDir(), fmt.Sprintf("azd-%s-*.%s", hookConfig.Name, ext))
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
	scriptBuilder.WriteString("\n")

	for _, line := range scriptFooter {
		scriptBuilder.WriteString(fmt.Sprintf("%s\n", line))
	}

	// Temp generated files are cleaned up automatically after script execution has completed.
	_, err = file.WriteString(scriptBuilder.String())
	if err != nil {
		return "", fmt.Errorf("failed writing hook file, %w", err)
	}

	// Update file permissions to grant exec permissions
	if err := file.Chmod(osutil.PermissionExecutableFile); err != nil {
		return "", fmt.Errorf("failed setting executable file permissions, %w", err)
	}

	return file.Name(), nil
}
