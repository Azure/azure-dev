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

	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
)

// HookType represents the execution timing of a hook relative to the
// associated command. Supported values are 'pre' and 'post'.
type HookType string

// HookPlatformType identifies the operating system platform for
// platform-specific hook overrides.
type HookPlatformType string

const (
	// Executes pre hooks
	HookTypePre HookType = "pre"
	// Execute post hooks
	HookTypePost        HookType         = "post"
	HookTypeNone        HookType         = ""
	HookPlatformWindows HookPlatformType = "windows"
	HookPlatformPosix   HookPlatformType = "posix"
)

var (
	// ErrScriptTypeUnknown indicates the language could not be inferred
	// from the script path and was not explicitly configured.
	ErrScriptTypeUnknown error = errors.New(
		"unable to determine hook language. " +
			"Ensure 'shell' or 'language' is set in your hook configuration, " +
			"or use a file with a recognized extension " +
			"(.sh, .ps1, .py, .js, .ts, .cs)",
	)
	// ErrRunRequired indicates the hook configuration is missing the mandatory 'run' field.
	ErrRunRequired error = errors.New(
		"'run' is required for every hook configuration. " +
			"Set 'run' to an inline script or a relative file path",
	)
	// ErrUnsupportedScriptType indicates the script file has an extension
	// that is not recognized and no explicit language or shell was set.
	ErrUnsupportedScriptType error = errors.New(
		"script type is not valid. " +
			"Supported extensions: .sh, .ps1, .py, .js, .ts, .cs. " +
			"Alternatively, set 'language' (e.g. language: python) " +
			"or 'shell' (e.g. shell: sh)",
	)
)

// Generic action function that may return an error
type InvokeFn func() error

// HookConfig defines the configuration for a single hook in azure.yaml.
// Hooks are lifecycle scripts that run before or after azd commands.
// Every hook is executed through a [tools.HookExecutor] resolved via IoC
// based on the hook's Language — including Bash, PowerShell, Python,
// and future executor types (JS, TS, DotNet).
type HookConfig struct {
	// When set, contains the resolved file path relative to the project or service
	path string
	// Stores a value whether or not this hook config has been previously validated
	validated bool
	// Stores the working directory set for this hook config
	cwd string
	// When set, contains the inline script content to execute
	script string
	// Indicates if the shell was automatically detected based on OS (used for warnings)
	usingDefaultShell bool

	// Internal name of the hook running for a given command
	Name string `yaml:",omitempty"`
	// The type of script hook (bash or powershell).
	// Retained as a string for backwards-compatible YAML
	// deserialization. After validate(), only Language is used.
	Shell string `yaml:"shell,omitempty"`
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
// the script location (inline vs. file path) and ensures that Language
// is always resolved to a concrete [language.ScriptLanguage] value.
//
// Language resolution priority:
//  1. Explicit Language field
//  2. Explicit Shell field (mapped to Language)
//  3. File extension via [language.InferLanguageFromPath]
//  4. OS default (Bash on Unix, PowerShell on Windows) for inline scripts
//
// After a successful call, Language is the single source of truth for
// executor selection and the hook is ready for execution.
func (hc *HookConfig) validate() error {
	if hc.validated {
		return nil
	}

	if hc.Run == "" {
		return ErrRunRequired
	}

	relativeCheckPath := strings.ReplaceAll(
		hc.Run, "/", string(os.PathSeparator),
	)
	fullCheckPath := relativeCheckPath
	if hc.cwd != "" {
		fullCheckPath = filepath.Join(hc.cwd, hc.Run)
	}

	stats, err := os.Stat(fullCheckPath)
	if err == nil && !stats.IsDir() {
		hc.path = relativeCheckPath
	} else {
		hc.script = hc.Run
	}

	// Language resolution — priority:
	// 1. explicit Language  2. explicit Shell  3. file extension
	// 4. OS default for inline scripts
	if hc.Language == language.ScriptLanguageUnknown {
		switch {
		case hc.Shell != "":
			hc.Language = shellToLanguage(hc.Shell)
		case hc.path != "":
			hc.Language = language.InferLanguageFromPath(
				hc.Run,
			)
		}
	}

	// Reject inline scripts for non-shell executors. Only Bash and
	// PowerShell executors support inline script content; other
	// executors require a file on disk.
	if hc.script != "" &&
		hc.Language != language.ScriptLanguageUnknown &&
		!hc.Language.IsShellLanguage() {
		return fmt.Errorf(
			"inline scripts are not supported for %s hooks. "+
				"Write your script to a file and set 'run' "+
				"to the file path "+
				"(e.g. run: ./hooks/my-script.py)",
			hc.Language,
		)
	}

	// Non-shell executors handle their own runtime and dependency
	// setup; no shell type resolution or temp script is needed.
	if hc.Language != language.ScriptLanguageUnknown &&
		!hc.Language.IsShellLanguage() {
		// Auto-infer Dir from the script's directory when not
		// explicitly set by the user.
		if hc.Dir == "" && hc.path != "" {
			hc.Dir = filepath.Dir(hc.path)
		}
		hc.validated = true
		return nil
	}

	// For inline scripts with no resolved language, default to the
	// OS-appropriate shell language.
	if hc.Language == language.ScriptLanguageUnknown &&
		hc.script != "" {
		hc.Language = defaultLanguageForOS()
		hc.usingDefaultShell = true
	}

	// For file-based hooks with an unrecognized extension, error.
	if hc.Language == language.ScriptLanguageUnknown {
		return fmt.Errorf(
			"script with file extension '%s' is not valid. "+
				"%w.",
			filepath.Ext(hc.path),
			ErrUnsupportedScriptType,
		)
	}

	hc.validated = true

	return nil
}

// IsPowerShellHook determines if a hook configuration uses PowerShell.
// It checks the resolved Language first, then falls back to the raw
// Shell field and file extension for hooks that have not yet been
// validated.
func (hc *HookConfig) IsPowerShellHook() bool {
	if hc.Language == language.ScriptLanguagePowerShell {
		return true
	}

	// Check raw Shell field (pre-validation).
	if hc.Shell == "pwsh" {
		return true
	}

	// Check file extension for unvalidated hooks.
	if hc.Run != "" {
		if strings.HasSuffix(strings.ToLower(hc.Run), ".ps1") {
			return true
		}
	}

	// Check OS-specific hook configurations
	if runtime.GOOS == "windows" && hc.Windows != nil {
		return hc.Windows.IsPowerShellHook()
	} else if (runtime.GOOS == "linux" ||
		runtime.GOOS == "darwin") && hc.Posix != nil {
		return hc.Posix.IsPowerShellHook()
	}

	return false
}

// IsUsingDefaultShell returns true if the hook is using the OS default shell
// because no shell was explicitly configured
func (hc *HookConfig) IsUsingDefaultShell() bool {
	return hc.usingDefaultShell
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

// defaultLanguageForOS returns the default shell language for the
// current operating system: PowerShell on Windows, Bash elsewhere.
func defaultLanguageForOS() language.ScriptLanguage {
	if runtime.GOOS == "windows" {
		return language.ScriptLanguagePowerShell
	}
	return language.ScriptLanguageBash
}

// shellToLanguage maps a raw shell string (from the YAML "shell"
// field) to the corresponding [language.ScriptLanguage]. Returns
// [language.ScriptLanguageUnknown] for unrecognized values.
func shellToLanguage(shell string) language.ScriptLanguage {
	switch shell {
	case "sh":
		return language.ScriptLanguageBash
	case "pwsh":
		return language.ScriptLanguagePowerShell
	default:
		return language.ScriptLanguageUnknown
	}
}
