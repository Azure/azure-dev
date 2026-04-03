// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package language

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestGetExecutor(t *testing.T) {
	mockRunner := &mockCommandRunner{}
	pythonCli := python.NewCli(mockRunner)

	tests := []struct {
		name       string
		language   ScriptLanguage
		wantErr    error  // sentinel error (checked via errors.Is)
		wantErrMsg string // substring in error message
		wantExec   bool   // true when a valid executor is expected
	}{
		{
			name:     "PythonReturnsExecutor",
			language: ScriptLanguagePython,
			wantExec: true,
		},
		{
			name:     "BashReturnsExecutor",
			language: ScriptLanguageBash,
			wantExec: true,
		},
		{
			name:     "PowerShellReturnsExecutor",
			language: ScriptLanguagePowerShell,
			wantExec: true,
		},
		{
			name:     "JavaScriptUnsupported",
			language: ScriptLanguageJavaScript,
			wantErr:  ErrUnsupportedLanguage,
		},
		{
			name:     "TypeScriptUnsupported",
			language: ScriptLanguageTypeScript,
			wantErr:  ErrUnsupportedLanguage,
		},
		{
			name:     "DotNetUnsupported",
			language: ScriptLanguageDotNet,
			wantErr:  ErrUnsupportedLanguage,
		},
		{
			name:       "UnknownReturnsError",
			language:   ScriptLanguageUnknown,
			wantErrMsg: "unknown script language",
		},
		{
			name:       "ArbitraryStringReturnsError",
			language:   ScriptLanguage("ruby"),
			wantErrMsg: "unknown script language",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor, err := GetExecutor(
				tt.language, mockRunner, pythonCli,
				"", "", nil,
			)

			switch {
			case tt.wantErr != nil:
				require.Error(t, err)
				assert.True(
					t,
					errors.Is(err, tt.wantErr),
					"expected error %v, got %v",
					tt.wantErr, err,
				)
				assert.Nil(t, executor)
			case tt.wantErrMsg != "":
				require.Error(t, err)
				assert.Contains(
					t, err.Error(), tt.wantErrMsg,
				)
				assert.Nil(t, executor)
			default:
				require.NoError(t, err)
			}

			if tt.wantExec {
				require.NotNil(t, executor)
			}
		})
	}
}

// mockCommandRunner is a minimal mock of [exec.CommandRunner]
// used to construct test dependencies without invoking real
// processes. Optional function fields allow tests to customize
// behavior when the zero-value defaults are insufficient.
type mockCommandRunner struct {
	lastRunArgs  exec.RunArgs
	runResult    exec.RunResult
	runErr       error
	toolInPathFn func(name string) error
}

func (m *mockCommandRunner) Run(
	_ context.Context,
	args exec.RunArgs,
) (exec.RunResult, error) {
	m.lastRunArgs = args
	return m.runResult, m.runErr
}

func (m *mockCommandRunner) RunList(
	_ context.Context,
	_ []string,
	_ exec.RunArgs,
) (exec.RunResult, error) {
	return m.runResult, m.runErr
}

func (m *mockCommandRunner) ToolInPath(name string) error {
	if m.toolInPathFn != nil {
		return m.toolInPathFn(name)
	}
	return nil
}
