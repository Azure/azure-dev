// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCommandRunner captures the RunArgs passed to Run().
type mockCommandRunner struct {
	capturedArgs exec.RunArgs
}

func (m *mockCommandRunner) Run(ctx context.Context, args exec.RunArgs) (exec.RunResult, error) {
	m.capturedArgs = args
	return exec.RunResult{}, nil
}

func (m *mockCommandRunner) RunList(ctx context.Context, commands []string, args exec.RunArgs) (exec.RunResult, error) {
	return exec.RunResult{}, nil
}

func (m *mockCommandRunner) ToolInPath(name string) error {
	return nil
}

// TestRunnerInvoke_GlobalFlagPropagation verifies that InvokeOptions fields
// (Debug, NoPrompt, Cwd, Environment) are correctly propagated as AZD_*
// environment variables to the extension child process.
//
// This tests the critical mapping path:
//
//	globalOptions.EnableDebugLogging → InvokeOptions.Debug    → AZD_DEBUG=true
//	globalOptions.NoPrompt           → InvokeOptions.NoPrompt → AZD_NO_PROMPT=true
//	globalOptions.Cwd                → InvokeOptions.Cwd      → AZD_CWD=<value>
//	globalOptions.EnvironmentName    → InvokeOptions.Environment → AZD_ENVIRONMENT=<value>
func TestRunnerInvoke_GlobalFlagPropagation(t *testing.T) {
	// Create a temp file to act as the extension binary
	tmpDir := t.TempDir()
	extBin := filepath.Join(tmpDir, "test-ext")
	require.NoError(t, os.WriteFile(extBin, []byte("#!/bin/sh\n"), 0o600)) //nolint:gosec

	// Point the user config dir to our temp dir so extensionPath resolves
	t.Setenv("AZD_CONFIG_DIR", tmpDir)

	mock := &mockCommandRunner{}
	runner := NewRunner(mock)

	ext := &Extension{
		Id:   "test-ext",
		Path: "test-ext",
	}

	tests := []struct {
		name       string
		options    *InvokeOptions
		expectEnvs map[string]string
		absentEnvs []string
	}{
		{
			name: "all global flags set",
			options: &InvokeOptions{
				Debug:       true,
				NoPrompt:    true,
				Cwd:         "/custom/dir",
				Environment: "dev",
			},
			expectEnvs: map[string]string{
				"AZD_DEBUG":       "true",
				"AZD_NO_PROMPT":   "true",
				"AZD_CWD":         "/custom/dir",
				"AZD_ENVIRONMENT": "dev",
			},
		},
		{
			name: "only environment set",
			options: &InvokeOptions{
				Environment: "staging",
			},
			expectEnvs: map[string]string{
				"AZD_ENVIRONMENT": "staging",
			},
			absentEnvs: []string{
				"AZD_DEBUG",
				"AZD_NO_PROMPT",
				"AZD_CWD",
			},
		},
		{
			name:    "no global flags",
			options: &InvokeOptions{},
			absentEnvs: []string{
				"AZD_DEBUG",
				"AZD_NO_PROMPT",
				"AZD_CWD",
				"AZD_ENVIRONMENT",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runner.Invoke(context.Background(), ext, tt.options)
			require.NoError(t, err)

			envMap := make(map[string]string)
			for _, e := range mock.capturedArgs.Env {
				for i := 0; i < len(e); i++ {
					if e[i] == '=' {
						envMap[e[:i]] = e[i+1:]
						break
					}
				}
			}

			for key, want := range tt.expectEnvs {
				got, ok := envMap[key]
				assert.True(t, ok,
					"expected env var %s to be set", key)
				assert.Equal(t, want, got,
					"env var %s", key)
			}

			for _, key := range tt.absentEnvs {
				_, ok := envMap[key]
				assert.False(t, ok,
					"expected env var %s to NOT be set", key)
			}
		})
	}
}
