// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInferKindFromPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected HookKind
	}{
		{
			name:     "Python",
			path:     "hooks/pre-deploy.py",
			expected: HookKindPython,
		},
		{
			name:     "JavaScript",
			path:     "hooks/pre-deploy.js",
			expected: HookKindJavaScript,
		},
		{
			name:     "TypeScript",
			path:     "hooks/pre-deploy.ts",
			expected: HookKindTypeScript,
		},
		{
			name:     "DotNet",
			path:     "hooks/pre-deploy.cs",
			expected: HookKindDotNet,
		},
		{
			name:     "Bash",
			path:     "hooks/pre-deploy.sh",
			expected: HookKindBash,
		},
		{
			name:     "PowerShell",
			path:     "hooks/pre-deploy.ps1",
			expected: HookKindPowerShell,
		},
		{
			name:     "UnknownTxt",
			path:     "hooks/readme.txt",
			expected: HookKindUnknown,
		},
		{
			name:     "UnknownGo",
			path:     "hooks/main.go",
			expected: HookKindUnknown,
		},
		{
			name:     "NoExtension",
			path:     "hooks/Makefile",
			expected: HookKindUnknown,
		},
		{
			name:     "EmptyPath",
			path:     "",
			expected: HookKindUnknown,
		},
		{
			name:     "CaseInsensitivePY",
			path:     "hooks/deploy.PY",
			expected: HookKindPython,
		},
		{
			name:     "CaseInsensitiveJs",
			path:     "hooks/deploy.Js",
			expected: HookKindJavaScript,
		},
		{
			name:     "CaseInsensitivePS1",
			path:     "hooks/deploy.PS1",
			expected: HookKindPowerShell,
		},
		{
			name:     "MultipleDots",
			path:     "hooks/pre.deploy.py",
			expected: HookKindPython,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InferKindFromPath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}
