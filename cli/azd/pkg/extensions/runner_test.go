// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ExtensionRunError
// ---------------------------------------------------------------------------

func TestExtensionRunError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		extensionId string
		inner       error
		want        string
	}{
		{
			name:        "BasicError",
			extensionId: "my-ext",
			inner:       errors.New("exit code 1"),
			want:        "extension 'my-ext' run failed: exit code 1",
		},
		{
			name:        "WrappedError",
			extensionId: "azd.test",
			inner:       errors.New("signal: killed"),
			want:        "extension 'azd.test' run failed: signal: killed",
		},
		{
			name:        "EmptyExtensionId",
			extensionId: "",
			inner:       errors.New("boom"),
			want:        "extension '' run failed: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := &ExtensionRunError{ExtensionId: tt.extensionId, Err: tt.inner}
			require.Equal(t, tt.want, e.Error())
		})
	}
}

func TestExtensionRunError_Unwrap(t *testing.T) {
	t.Parallel()

	inner := errors.New("root cause")
	e := &ExtensionRunError{ExtensionId: "test-ext", Err: inner}

	require.ErrorIs(t, e, inner)
	require.Equal(t, inner, e.Unwrap())
}

func TestExtensionRunError_NilInner(t *testing.T) {
	t.Parallel()

	e := &ExtensionRunError{ExtensionId: "ext", Err: nil}
	require.Contains(t, e.Error(), "ext")
	require.Nil(t, e.Unwrap())
}

// ---------------------------------------------------------------------------
// NewRunner
// ---------------------------------------------------------------------------

func TestRunner_NewRunner(t *testing.T) {
	t.Parallel()

	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)
	require.NotNil(t, runner)
}

// ---------------------------------------------------------------------------
// Runner.Invoke
// ---------------------------------------------------------------------------

// setupConfigAndExtension creates a temp config dir, sets AZD_CONFIG_DIR, and
// creates a fake extension binary at the expected path. Returns the config dir
// and a minimal Extension value whose Path resolves correctly.
//
// Tests that call this helper must NOT use t.Parallel() because t.Setenv
// mutates process-global state.
func setupConfigAndExtension(t *testing.T) (string, *Extension) {
	t.Helper()

	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	extRelPath := filepath.Join("extensions", "test-ext", "bin", "test-ext")
	extFullPath := filepath.Join(configDir, extRelPath)

	require.NoError(t, os.MkdirAll(filepath.Dir(extFullPath), 0o755))
	require.NoError(t, os.WriteFile(extFullPath, []byte("fake-binary"), 0o600))

	ext := &Extension{
		Id:   "test-ext",
		Path: extRelPath,
	}

	return configDir, ext
}

func TestRunner_Invoke_Success(t *testing.T) {
	configDir, ext := setupConfigAndExtension(t)
	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	expectedPath := filepath.Join(configDir, ext.Path)

	cmdRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == expectedPath
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 0, Stdout: "ok"}, nil
	})

	result, err := runner.Invoke(t.Context(), ext, &InvokeOptions{
		Args: []string{"hello", "world"},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 0, result.ExitCode)
	require.Equal(t, "ok", result.Stdout)
}

func TestRunner_Invoke_MissingExtensionPath(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AZD_CONFIG_DIR", configDir)

	ext := &Extension{
		Id:   "nonexistent",
		Path: filepath.Join("extensions", "missing", "binary"),
	}

	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	result, err := runner.Invoke(t.Context(), ext, &InvokeOptions{})
	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "extension path")
	require.Contains(t, err.Error(), "not found")
}

func TestRunner_Invoke_ArgsPassedThrough(t *testing.T) {
	configDir, ext := setupConfigAndExtension(t)
	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	expectedPath := filepath.Join(configDir, ext.Path)
	wantArgs := []string{"serve", "--port", "8080"}

	var capturedArgs exec.RunArgs

	cmdRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == expectedPath
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args
		return exec.RunResult{ExitCode: 0}, nil
	})

	_, err := runner.Invoke(t.Context(), ext, &InvokeOptions{
		Args: wantArgs,
	})
	require.NoError(t, err)
	require.Equal(t, wantArgs, capturedArgs.Args)
}

func TestRunner_Invoke_EnvVariablePropagation(t *testing.T) {
	tests := []struct {
		name    string
		options InvokeOptions
		wantEnv []string
	}{
		{
			name: "DebugTrue",
			options: InvokeOptions{
				Debug: true,
			},
			wantEnv: []string{"AZD_DEBUG=true"},
		},
		{
			name: "NoPromptTrue",
			options: InvokeOptions{
				NoPrompt: true,
			},
			wantEnv: []string{"AZD_NO_PROMPT=true"},
		},
		{
			name: "CwdSet",
			options: InvokeOptions{
				Cwd: "/my/project",
			},
			wantEnv: []string{"AZD_CWD=/my/project"},
		},
		{
			name: "EnvironmentSet",
			options: InvokeOptions{
				Environment: "dev",
			},
			wantEnv: []string{"AZD_ENVIRONMENT=dev"},
		},
		{
			name: "AllFlags",
			options: InvokeOptions{
				Debug:       true,
				NoPrompt:    true,
				Cwd:         "/work",
				Environment: "staging",
			},
			wantEnv: []string{
				"AZD_DEBUG=true",
				"AZD_NO_PROMPT=true",
				"AZD_CWD=/work",
				"AZD_ENVIRONMENT=staging",
			},
		},
		{
			name:    "NoFlags",
			options: InvokeOptions{},
			wantEnv: nil,
		},
		{
			name: "ExistingEnvPreserved",
			options: InvokeOptions{
				Env:   []string{"CUSTOM_VAR=hello"},
				Debug: true,
			},
			wantEnv: []string{"CUSTOM_VAR=hello", "AZD_DEBUG=true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each subtest gets its own config dir via setupConfigAndExtension,
			// which calls t.Setenv - cannot use t.Parallel().
			_, ext := setupConfigAndExtension(t)
			cmdRunner := mockexec.NewMockCommandRunner()
			runner := NewRunner(cmdRunner)

			var capturedArgs exec.RunArgs

			cmdRunner.When(func(args exec.RunArgs, command string) bool {
				return true
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				capturedArgs = args
				return exec.RunResult{ExitCode: 0}, nil
			})

			// Make a copy so the table entry isn't mutated across iterations.
			opts := tt.options
			_, err := runner.Invoke(t.Context(), ext, &opts)
			require.NoError(t, err)

			if tt.wantEnv == nil {
				require.Empty(t, capturedArgs.Env)
			} else {
				for _, expected := range tt.wantEnv {
					require.True(t,
						slices.Contains(capturedArgs.Env, expected),
						"expected env var %q not found in %v", expected, capturedArgs.Env,
					)
				}
				require.Len(t, capturedArgs.Env, len(tt.wantEnv))
			}
		})
	}
}

func TestRunner_Invoke_InteractiveMode(t *testing.T) {
	_, ext := setupConfigAndExtension(t)
	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	var capturedArgs exec.RunArgs

	cmdRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args
		return exec.RunResult{ExitCode: 0}, nil
	})

	_, err := runner.Invoke(t.Context(), ext, &InvokeOptions{
		Interactive: true,
	})
	require.NoError(t, err)
	require.True(t, capturedArgs.Interactive)
	// In interactive mode, custom streams should not be set on RunArgs
	require.Nil(t, capturedArgs.StdIn)
	require.Nil(t, capturedArgs.StdOut)
}

func TestRunner_Invoke_NonInteractiveWithStreams(t *testing.T) {
	_, ext := setupConfigAndExtension(t)
	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	var capturedArgs exec.RunArgs

	stdinBuf := strings.NewReader("input data")
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}

	cmdRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args
		return exec.RunResult{ExitCode: 0}, nil
	})

	_, err := runner.Invoke(t.Context(), ext, &InvokeOptions{
		Interactive: false,
		StdIn:       stdinBuf,
		StdOut:      stdoutBuf,
		StdErr:      stderrBuf,
	})
	require.NoError(t, err)
	require.False(t, capturedArgs.Interactive)
	require.Equal(t, stdinBuf, capturedArgs.StdIn)
	require.Equal(t, stdoutBuf, capturedArgs.StdOut)
	// RunArgs.Stderr (note: lowercase 'e') maps to WithStdErr
	require.Equal(t, stderrBuf, capturedArgs.Stderr)
}

func TestRunner_Invoke_NonInteractiveNilStreams(t *testing.T) {
	_, ext := setupConfigAndExtension(t)
	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	var capturedArgs exec.RunArgs

	cmdRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedArgs = args
		return exec.RunResult{ExitCode: 0}, nil
	})

	_, err := runner.Invoke(t.Context(), ext, &InvokeOptions{
		Interactive: false,
		StdIn:       nil,
		StdOut:      nil,
		StdErr:      nil,
	})
	require.NoError(t, err)
	require.False(t, capturedArgs.Interactive)
	require.Nil(t, capturedArgs.StdIn)
	require.Nil(t, capturedArgs.StdOut)
	require.Nil(t, capturedArgs.Stderr)
}

func TestRunner_Invoke_CommandError_WrapsInExtensionRunError(t *testing.T) {
	_, ext := setupConfigAndExtension(t)
	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	cmdError := errors.New("exit status 42")

	cmdRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 42, Stderr: "something broke"}, cmdError
	})

	result, err := runner.Invoke(t.Context(), ext, &InvokeOptions{})
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, 42, result.ExitCode)

	// Verify we get an ExtensionRunError
	var runErr *ExtensionRunError
	require.ErrorAs(t, err, &runErr)
	require.Equal(t, ext.Id, runErr.ExtensionId)
	require.ErrorIs(t, runErr, cmdError)
}

func TestRunner_Invoke_ExtensionPathResolution(t *testing.T) {
	configDir, ext := setupConfigAndExtension(t)
	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	expectedCmd := filepath.Join(configDir, ext.Path)
	var capturedCmd string

	cmdRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		capturedCmd = args.Cmd
		return exec.RunResult{ExitCode: 0}, nil
	})

	_, err := runner.Invoke(t.Context(), ext, &InvokeOptions{})
	require.NoError(t, err)
	require.Equal(t, expectedCmd, capturedCmd)
}

func TestRunner_Invoke_EnsureInit_Called(t *testing.T) {
	_, ext := setupConfigAndExtension(t)
	cmdRunner := mockexec.NewMockCommandRunner()
	runner := NewRunner(cmdRunner)

	// Extension starts uninitialized
	require.False(t, ext.initialized)

	cmdRunner.When(func(args exec.RunArgs, command string) bool {
		return true
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{ExitCode: 0}, nil
	})

	_, err := runner.Invoke(t.Context(), ext, &InvokeOptions{})
	require.NoError(t, err)

	// After Invoke, extension should be initialized
	require.True(t, ext.initialized)
}
