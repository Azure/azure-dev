// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
)

// ScriptLanguage identifies the programming language of a hook script.
// The string value matches the token users write in the "language" field
// of azure.yaml hook configurations.
type ScriptLanguage string

const (
	// ScriptLanguageUnknown indicates the language could not be
	// determined from the file extension or explicit configuration.
	ScriptLanguageUnknown ScriptLanguage = ""
	// ScriptLanguageBash identifies Bash shell scripts (.sh files).
	ScriptLanguageBash ScriptLanguage = "sh"
	// ScriptLanguagePowerShell identifies PowerShell scripts (.ps1 files).
	ScriptLanguagePowerShell ScriptLanguage = "pwsh"
	// ScriptLanguageJavaScript identifies JavaScript scripts (.js files).
	// Not yet supported — returns [ErrUnsupportedLanguage] from [GetExecutor].
	ScriptLanguageJavaScript ScriptLanguage = "js"
	// ScriptLanguageTypeScript identifies TypeScript scripts (.ts files).
	// Not yet supported — returns [ErrUnsupportedLanguage] from [GetExecutor].
	ScriptLanguageTypeScript ScriptLanguage = "ts"
	// ScriptLanguagePython identifies Python scripts (.py files).
	ScriptLanguagePython ScriptLanguage = "python"
	// ScriptLanguageDotNet identifies .NET (C#) scripts (.cs files).
	// Not yet supported — returns [ErrUnsupportedLanguage] from [GetExecutor].
	ScriptLanguageDotNet ScriptLanguage = "dotnet"
)

// ErrUnsupportedLanguage is returned by [GetExecutor] when the
// requested [ScriptLanguage] is recognized but no [ScriptExecutor]
// implementation exists yet (e.g. JavaScript, TypeScript, DotNet).
var ErrUnsupportedLanguage = errors.New(
	"language is not yet supported; supported languages: python. " +
		"JavaScript, TypeScript, and .NET support is planned",
)

// ErrShellLanguage is returned by [GetExecutor] when the caller
// requests an executor for a shell language (Bash or PowerShell).
// Shell scripts are handled by the existing shell script runner in
// [pkg/ext] and do not use the [ScriptExecutor] pipeline.
var ErrShellLanguage = errors.New(
	"shell languages (sh, pwsh) are handled by the existing " +
		"shell script runner, not the language executor pipeline",
)

// ScriptExecutor defines the interface for language-specific hook
// script preparation and execution.
type ScriptExecutor interface {
	// Language returns the script language this executor handles.
	Language() ScriptLanguage

	// Prepare performs pre-execution steps such as runtime
	// validation, dependency installation, or build steps.
	Prepare(ctx context.Context, scriptPath string) error

	// Execute runs the script at the given path and returns the
	// result. The signature is compatible with [tools.Script].
	Execute(
		ctx context.Context,
		scriptPath string,
		options tools.ExecOptions,
	) (exec.RunResult, error)
}

// InferLanguageFromPath determines the [ScriptLanguage] from the
// file extension of the given path. Extension matching is
// case-insensitive. The following extensions are recognized:
//
//   - .py  → [ScriptLanguagePython]
//   - .js  → [ScriptLanguageJavaScript]
//   - .ts  → [ScriptLanguageTypeScript]
//   - .cs  → [ScriptLanguageDotNet]
//   - .sh  → [ScriptLanguageBash]
//   - .ps1 → [ScriptLanguagePowerShell]
//
// Returns [ScriptLanguageUnknown] for unrecognized extensions.
func InferLanguageFromPath(path string) ScriptLanguage {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".py":
		return ScriptLanguagePython
	case ".js":
		return ScriptLanguageJavaScript
	case ".ts":
		return ScriptLanguageTypeScript
	case ".cs":
		return ScriptLanguageDotNet
	case ".sh":
		return ScriptLanguageBash
	case ".ps1":
		return ScriptLanguagePowerShell
	default:
		return ScriptLanguageUnknown
	}
}

// GetExecutor returns a [ScriptExecutor] for the given language.
//
// Phase 1 supports only Python. JavaScript, TypeScript, and DotNet
// return [ErrUnsupportedLanguage]. Bash and PowerShell return
// [ErrShellLanguage] because they are handled by the existing shell
// script runner.
//
// The boundaryDir limits project file discovery during Prepare; cwd
// sets the working directory for script execution; envVars are
// forwarded to all child processes.
func GetExecutor(
	lang ScriptLanguage,
	commandRunner exec.CommandRunner,
	pythonCli *python.Cli,
	boundaryDir string,
	cwd string,
	envVars []string,
) (ScriptExecutor, error) {
	switch lang {
	case ScriptLanguagePython:
		return newPythonExecutor(
			commandRunner, pythonCli,
			boundaryDir, cwd, envVars,
		), nil
	case ScriptLanguageJavaScript,
		ScriptLanguageTypeScript,
		ScriptLanguageDotNet:
		return nil, fmt.Errorf(
			"%w: %s", ErrUnsupportedLanguage, lang,
		)
	case ScriptLanguageBash, ScriptLanguagePowerShell:
		return nil, fmt.Errorf(
			"%w: %s", ErrShellLanguage, lang,
		)
	default:
		return nil, fmt.Errorf(
			"unknown script language: %q", string(lang),
		)
	}
}
