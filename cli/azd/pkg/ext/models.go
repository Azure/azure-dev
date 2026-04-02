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

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
)

// HookType represents the execution timing of a hook relative to the
// associated command. Supported values are 'pre' and 'post'.
type HookType string

// HookPlatformType identifies the operating system platform for
// platform-specific hook overrides.
type HookPlatformType string

// ShellType identifies the shell used to execute hook scripts.
type ShellType string

// ScriptLocation indicates whether a hook script is defined inline
// in azure.yaml or references an external file path.
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
	// ErrScriptTypeUnknown indicates the shell type could not be inferred from
	// the script path and was not explicitly configured.
	ErrScriptTypeUnknown error = errors.New(
		"unable to determine script type. " +
			"Ensure 'shell' is set to 'sh' or 'pwsh' in your hook configuration, " +
			"or use a file with a .sh or .ps1 extension",
	)
	// ErrRunRequired indicates the hook configuration is missing the mandatory 'run' field.
	ErrRunRequired error = errors.New(
		"'run' is required for every hook configuration. " +
			"Set 'run' to an inline script or a relative file path",
	)
	// ErrUnsupportedScriptType indicates the script file has an extension that is not
	// a recognized shell type (.sh or .ps1) and no explicit language or shell was set.
	ErrUnsupportedScriptType error = errors.New(
		"script type is not valid. Only '.sh' and '.ps1' are supported for shell hooks. " +
			"For other languages, set the 'language' field (e.g. language: python)",
	)
)

// Generic action function that may return an error
type InvokeFn func() error

// HookConfig defines the configuration for a single hook in azure.yaml.
// Hooks are lifecycle scripts that run before or after azd commands.
// They may be shell scripts (sh/pwsh) executed via the shell runner,
// or programming-language scripts (Python, JS, TS, DotNet) executed
// via the [language.ScriptExecutor] pipeline.
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
	// Language specifies the programming language of the hook script.
	// Allowed values: "sh", "pwsh", "js", "ts", "python", "dotnet".
	// When empty, the language is auto-detected from the file extension
	// of the run path (e.g. .py → python, .ps1 → pwsh). If both
	// Language and Shell are empty and run references a file, the
	// extension is used. For inline scripts, Shell or Language must be
	// set explicitly.
	Language language.ScriptLanguage `yaml:"language,omitempty" json:"language,omitempty"`
	// Dir specifies the working directory for language hook execution,
	// used as the project context for dependency installation and builds.
	// When empty, defaults to the directory containing the script
	// referenced by the run field. Only set this when the project root
	// differs from the script's directory.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
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

// validate normalizes and validates the hook configuration. It resolves
// the script location (inline vs. file path), infers the Language from
// the Shell or file extension when not explicitly set, and rejects
// invalid combinations (e.g. inline scripts for non-shell languages).
// After a successful call, the hook is ready for execution.
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

	// Language resolution — priority:
	// 1. explicit Language  2. explicit Shell  3. file extension
	if hc.Language == language.ScriptLanguageUnknown {
		switch {
		case hc.Shell != ScriptTypeUnknown:
			hc.Language = shellToLanguage(hc.Shell)
		case hc.location == ScriptLocationPath:
			hc.Language = language.InferLanguageFromPath(hc.Run)
		}
	}

	// Reject inline scripts for non-shell (language) hooks.
	if hc.location == ScriptLocationInline && hc.IsLanguageHook() {
		return fmt.Errorf(
			"inline scripts are not supported for %s hooks. "+
				"Write your script to a file and set 'run' to the file path "+
				"(e.g. run: ./hooks/my-script.py)",
			hc.Language,
		)
	}

	// Language hooks are executed by a language-specific executor;
	// no shell type resolution or temp script is needed.
	if hc.IsLanguageHook() {
		// Auto-infer Dir from the script's directory when not
		// explicitly set by the user.
		if hc.Dir == "" && hc.location == ScriptLocationPath {
			hc.Dir = filepath.Dir(hc.path)
		}
		hc.validated = true
		return nil
	}

	// If Language resolved to a shell variant but Shell is unset,
	// derive Shell so the existing shell execution path works.
	if hc.Shell == ScriptTypeUnknown {
		switch hc.Language {
		case language.ScriptLanguageBash:
			hc.Shell = ShellTypeBash
		case language.ScriptLanguagePowerShell:
			hc.Shell = ShellTypePowershell
		}
	}

	// --- existing shell behavior (unchanged) ---

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

	// Backfill Language from resolved Shell for shell-based hooks.
	if hc.Language == language.ScriptLanguageUnknown {
		hc.Language = shellToLanguage(hc.Shell)
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

// IsLanguageHook returns true when this hook targets a programming
// language (Python, JavaScript, TypeScript, or DotNet) rather than a
// shell (Bash or PowerShell). Language hooks are executed through the
// [language.ScriptExecutor] pipeline instead of the shell runner.
func (hc *HookConfig) IsLanguageHook() bool {
	switch hc.Language {
	case language.ScriptLanguagePython,
		language.ScriptLanguageJavaScript,
		language.ScriptLanguageTypeScript,
		language.ScriptLanguageDotNet:
		return true
	default:
		return false
	}
}

// InferHookType extracts the hook timing prefix ("pre" or "post")
// from a hook name and returns the remaining command name. For
// example, "preprovision" → (HookTypePre, "provision").
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

// shellToLanguage maps a [ShellType] to the corresponding
// [language.ScriptLanguage]. Returns [language.ScriptLanguageUnknown]
// for unrecognized shell types.
func shellToLanguage(shell ShellType) language.ScriptLanguage {
	switch shell {
	case ShellTypeBash:
		return language.ScriptLanguageBash
	case ShellTypePowershell:
		return language.ScriptLanguagePowerShell
	default:
		return language.ScriptLanguageUnknown
	}
}

// inferScriptTypeFromFilePath returns the [ShellType] for a file
// based on its extension. Only .sh and .ps1 are valid shell types;
// other extensions return [ErrUnsupportedScriptType].
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
