// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInferLanguageFromPath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected ScriptLanguage
	}{
		{
			name:     "Python",
			path:     "hooks/pre-deploy.py",
			expected: ScriptLanguagePython,
		},
		{
			name:     "JavaScript",
			path:     "hooks/pre-deploy.js",
			expected: ScriptLanguageJavaScript,
		},
		{
			name:     "TypeScript",
			path:     "hooks/pre-deploy.ts",
			expected: ScriptLanguageTypeScript,
		},
		{
			name:     "DotNet",
			path:     "hooks/pre-deploy.cs",
			expected: ScriptLanguageDotNet,
		},
		{
			name:     "Bash",
			path:     "hooks/pre-deploy.sh",
			expected: ScriptLanguageBash,
		},
		{
			name:     "PowerShell",
			path:     "hooks/pre-deploy.ps1",
			expected: ScriptLanguagePowerShell,
		},
		{
			name:     "UnknownTxt",
			path:     "hooks/readme.txt",
			expected: ScriptLanguageUnknown,
		},
		{
			name:     "UnknownGo",
			path:     "hooks/main.go",
			expected: ScriptLanguageUnknown,
		},
		{
			name:     "NoExtension",
			path:     "hooks/Makefile",
			expected: ScriptLanguageUnknown,
		},
		{
			name:     "EmptyPath",
			path:     "",
			expected: ScriptLanguageUnknown,
		},
		{
			name:     "CaseInsensitivePY",
			path:     "hooks/deploy.PY",
			expected: ScriptLanguagePython,
		},
		{
			name:     "CaseInsensitiveJs",
			path:     "hooks/deploy.Js",
			expected: ScriptLanguageJavaScript,
		},
		{
			name:     "CaseInsensitivePS1",
			path:     "hooks/deploy.PS1",
			expected: ScriptLanguagePowerShell,
		},
		{
			name:     "MultipleDots",
			path:     "hooks/pre.deploy.py",
			expected: ScriptLanguagePython,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InferLanguageFromPath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}
