// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"path/filepath"
	"strings"
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
	// Not yet supported — IoC resolution will fail with a descriptive error.
	ScriptLanguageJavaScript ScriptLanguage = "js"
	// ScriptLanguageTypeScript identifies TypeScript scripts (.ts files).
	// Not yet supported — IoC resolution will fail with a descriptive error.
	ScriptLanguageTypeScript ScriptLanguage = "ts"
	// ScriptLanguagePython identifies Python scripts (.py files).
	ScriptLanguagePython ScriptLanguage = "python"
	// ScriptLanguageDotNet identifies .NET (C#) scripts (.cs files).
	// Not yet supported — IoC resolution will fail with a descriptive error.
	ScriptLanguageDotNet ScriptLanguage = "dotnet"
)

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
