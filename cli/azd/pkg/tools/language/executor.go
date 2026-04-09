// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"path/filepath"
	"strings"
)

// HookKind identifies the executor type of a hook script.
// The string value matches the token users write in the "kind" field
// of azure.yaml hook configurations.
type HookKind string

const (
	// HookKindUnknown indicates the kind could not be
	// determined from the file extension or explicit configuration.
	HookKindUnknown HookKind = ""
	// HookKindBash identifies Bash shell scripts (.sh files).
	HookKindBash HookKind = "sh"
	// HookKindPowerShell identifies PowerShell scripts (.ps1 files).
	HookKindPowerShell HookKind = "pwsh"
	// HookKindJavaScript identifies JavaScript scripts (.js files).
	HookKindJavaScript HookKind = "js"
	// HookKindTypeScript identifies TypeScript scripts (.ts files).
	HookKindTypeScript HookKind = "ts"
	// HookKindPython identifies Python scripts (.py files).
	HookKindPython HookKind = "python"
	// HookKindDotNet identifies .NET (C#) scripts (.cs files).
	// Not yet supported — IoC resolution will fail with a descriptive error.
	HookKindDotNet HookKind = "dotnet"
)

// IsShell reports whether k is one of the built-in shell
// kinds (Bash or PowerShell). Shell hooks support inline scripts
// and are executed directly by the OS shell. Non-shell kinds
// (Python, JS, TS, DotNet) require a file on disk and are executed
// through dedicated [tools.HookExecutor] implementations.
func (k HookKind) IsShell() bool {
	return k == HookKindBash ||
		k == HookKindPowerShell
}

// InferKindFromPath determines the [HookKind] from the
// file extension of the given path. Extension matching is
// case-insensitive. The following extensions are recognized:
//
//   - .py  → [HookKindPython]
//   - .js  → [HookKindJavaScript]
//   - .ts  → [HookKindTypeScript]
//   - .cs  → [HookKindDotNet]
//   - .sh  → [HookKindBash]
//   - .ps1 → [HookKindPowerShell]
//
// Returns [HookKindUnknown] for unrecognized extensions.
func InferKindFromPath(path string) HookKind {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".py":
		return HookKindPython
	case ".js":
		return HookKindJavaScript
	case ".ts":
		return HookKindTypeScript
	case ".cs":
		return HookKindDotNet
	case ".sh":
		return HookKindBash
	case ".ps1":
		return HookKindPowerShell
	default:
		return HookKindUnknown
	}
}
