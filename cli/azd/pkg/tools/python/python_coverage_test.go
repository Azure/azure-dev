// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package python

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/blang/semver/v4"
	"github.com/stretchr/testify/require"
)

func Test_Name(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	require.Equal(t, "Python CLI", cli.Name())
}

func Test_InstallUrl(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)
	require.Equal(t, "https://wiki.python.org/moin/BeginnersGuide/Download", cli.InstallUrl())
}

func Test_VersionInfo(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	vi := cli.versionInfo()
	require.Equal(t, semver.Version{Major: 3, Minor: 7, Patch: 6}, vi.MinimumVersion)
	require.Contains(t, vi.UpdateCommand, "https://www.python.org/downloads/")
}

// mockPythonInPath mocks ToolInPath so checkPath succeeds deterministically.
// Returns the python command string that checkPath will resolve to.
func mockPythonInPath(mockContext *mocks.MockContext) string {
	if runtime.GOOS == "windows" {
		mockContext.CommandRunner.MockToolInPath("py", nil)
		return "py"
	}
	mockContext.CommandRunner.MockToolInPath("python3", nil)
	return "python3"
}

// mockPythonNotInPath mocks ToolInPath so checkPath fails on all candidates.
func mockPythonNotInPath(mockContext *mocks.MockContext) {
	notFound := errors.New("not found in PATH")
	if runtime.GOOS == "windows" {
		mockContext.CommandRunner.MockToolInPath("py", notFound)
		mockContext.CommandRunner.MockToolInPath("python", notFound)
	} else {
		mockContext.CommandRunner.MockToolInPath("python3", notFound)
	}
}

func Test_CheckInstalled_Success(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	pyCmd := mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return args.Cmd == pyCmd && strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "Python 3.10.0", ""), nil
	})

	err := cli.CheckInstalled(*mockContext.Context)
	require.NoError(t, err)
}

func Test_CheckInstalled_ExactMinimumVersion(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	// 3.7.6 is exactly the minimum — should pass
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "Python 3.7.6", ""), nil
	})

	err := cli.CheckInstalled(*mockContext.Context)
	require.NoError(t, err)
}

func Test_CheckInstalled_VersionTooOld(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "Python 3.6.0", ""), nil
	})

	err := cli.CheckInstalled(*mockContext.Context)
	require.Error(t, err)

	var semverErr *tools.ErrSemver
	require.True(t, errors.As(err, &semverErr))
	require.Equal(t, "Python CLI", semverErr.ToolName)
	require.Equal(t, semver.Version{Major: 3, Minor: 7, Patch: 6}, semverErr.VersionInfo.MinimumVersion)
}

func Test_CheckInstalled_NotInstalled(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonNotInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	err := cli.CheckInstalled(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func Test_CheckInstalled_VersionCommandFails(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{}, errors.New("command execution failed")
	})

	err := cli.CheckInstalled(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "checking Python CLI version")
}

func Test_CheckInstalled_GarbageVersionOutput(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "--version")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "not a version string at all", ""), nil
	})

	err := cli.CheckInstalled(*mockContext.Context)
	require.Error(t, err)
	require.Contains(t, err.Error(), "converting to semver version fails")
}

func Test_InstallProject_Success(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	pyCmd := mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	var capturedArgs exec.RunArgs
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		capturedArgs = args
		return strings.Contains(command, "pip install .")
	}).Respond(exec.NewRunResult(0, "", ""))

	err := cli.InstallProject(*mockContext.Context, tempDir, ".venv", nil)
	require.NoError(t, err)
	require.Equal(t, tempDir, capturedArgs.Cwd)
	// RunList is called with [activationCmd, "py -m pip install ."]
	require.Len(t, capturedArgs.Args, 2)
	require.Contains(t, capturedArgs.Args[0], "activate")
	require.Equal(t, fmt.Sprintf("%s -m pip install .", pyCmd), capturedArgs.Args[1])
}

func Test_InstallProject_Error(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "pip install .")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{}, errors.New("pip install failed")
	})

	err := cli.InstallProject(*mockContext.Context, tempDir, ".venv", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to install project from pyproject.toml")
}

func Test_InstallProject_PassesEnvVars(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	var capturedArgs exec.RunArgs
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		capturedArgs = args
		return strings.Contains(command, "pip install .")
	}).Respond(exec.NewRunResult(0, "", ""))

	env := []string{"PIP_INDEX_URL=https://pypi.example.com/simple"}
	err := cli.InstallProject(*mockContext.Context, tempDir, ".venv", env)
	require.NoError(t, err)
	require.Equal(t, env, capturedArgs.Env)
}

func Test_Run_Error(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "activate")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{}, errors.New("script execution failed")
	})

	result, err := cli.Run(*mockContext.Context, tempDir, ".venv", nil, "script.py")
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "failed to run Python script")
}

func Test_Run_WithEnvVars(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	var capturedArgs exec.RunArgs
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		capturedArgs = args
		return strings.Contains(command, "script.py")
	}).Respond(exec.NewRunResult(0, "output", ""))

	env := []string{"MY_VAR=value"}
	result, err := cli.Run(*mockContext.Context, tempDir, ".venv", env, "script.py", "--flag")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, env, capturedArgs.Env)
	require.Equal(t, 0, result.ExitCode)
}

func Test_Run_NotInstalled(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonNotInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	result, err := cli.Run(*mockContext.Context, tempDir, ".venv", nil, "script.py")
	require.Error(t, err)
	require.Nil(t, result)
}

func Test_InstallRequirements_Error(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "requirements.txt")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{}, errors.New("pip install -r failed")
	})

	err := cli.InstallRequirements(*mockContext.Context, tempDir, ".venv", "requirements.txt", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to install requirements for project")
}

func Test_CreateVirtualEnv_Error(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "-m venv")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.RunResult{}, errors.New("venv creation failed")
	})

	err := cli.CreateVirtualEnv(*mockContext.Context, tempDir, ".venv", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to create virtual Python environment")
}

func Test_CreateVirtualEnv_NotInstalled(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonNotInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	err := cli.CreateVirtualEnv(*mockContext.Context, tempDir, ".venv", nil)
	require.Error(t, err)
}

func Test_CheckPath_WindowsFallback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific test")
	}

	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	// "py" not found, but "python" is available → should fall back to "python"
	mockContext.CommandRunner.MockToolInPath("py", errors.New("py not found"))
	mockContext.CommandRunner.MockToolInPath("python", nil)

	pyString, err := cli.checkPath()
	require.NoError(t, err)
	require.Equal(t, "python", pyString)
}

func Test_CheckPath_WindowsBothNotFound(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific test")
	}

	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.MockToolInPath("py", errors.New("py not found"))
	mockContext.CommandRunner.MockToolInPath("python", errors.New("python not found"))

	_, err := cli.checkPath()
	require.Error(t, err)
	// Last error should be from "python" attempt
	require.Contains(t, err.Error(), "python not found")
}

func Test_CheckPath_WindowsPrefersPy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific test")
	}

	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	// Both available, but "py" should be preferred
	mockContext.CommandRunner.MockToolInPath("py", nil)
	mockContext.CommandRunner.MockToolInPath("python", nil)

	pyString, err := cli.checkPath()
	require.NoError(t, err)
	require.Equal(t, "py", pyString)
}

func Test_CreateVirtualEnv_WithEnvVars(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	var capturedArgs exec.RunArgs
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		capturedArgs = args
		return strings.Contains(command, "-m venv")
	}).Respond(exec.NewRunResult(0, "", ""))

	env := []string{"VIRTUAL_ENV=test"}
	err := cli.CreateVirtualEnv(*mockContext.Context, tempDir, ".venv", env)
	require.NoError(t, err)
	require.Equal(t, env, capturedArgs.Env)
}

// ---------------------------------------------------------------------------
// ResolveCommand tests
// ---------------------------------------------------------------------------

func Test_ResolveCommand_Success(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	pyCmd := mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	cmd, err := cli.ResolveCommand()
	require.NoError(t, err)
	require.Equal(t, pyCmd, cmd)
}

func Test_ResolveCommand_NotInstalled(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonNotInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	_, err := cli.ResolveCommand()
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// EnsureVirtualEnv tests
// ---------------------------------------------------------------------------

func Test_EnsureVirtualEnv_CreatesWhenMissing(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(
		func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "-m venv")
		},
	).Respond(exec.NewRunResult(0, "", ""))

	err := cli.EnsureVirtualEnv(
		*mockContext.Context, tempDir, ".venv", nil,
	)
	require.NoError(t, err)
}

func Test_EnsureVirtualEnv_SkipsWhenExists(t *testing.T) {
	tempDir := t.TempDir()
	venvDir := filepath.Join(tempDir, ".venv")
	require.NoError(t, os.MkdirAll(venvDir, 0o700))

	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	// No mock for CreateVirtualEnv — must not be called.
	err := cli.EnsureVirtualEnv(
		*mockContext.Context, tempDir, ".venv", nil,
	)
	require.NoError(t, err)
}

func Test_EnsureVirtualEnv_ErrorWhenFileNotDir(t *testing.T) {
	tempDir := t.TempDir()
	venvPath := filepath.Join(tempDir, ".venv")
	require.NoError(t, os.WriteFile(
		venvPath, []byte("file"), 0o600,
	))

	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	err := cli.EnsureVirtualEnv(
		*mockContext.Context, tempDir, ".venv", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func Test_EnsureVirtualEnv_CreateFails(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(
		func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "-m venv")
		},
	).RespondFn(
		func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.RunResult{},
				errors.New("venv module not found")
		},
	)

	err := cli.EnsureVirtualEnv(
		*mockContext.Context, tempDir, "my_env", nil,
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "venv module not found")
}

// ---------------------------------------------------------------------------
// InstallDependencies tests
// ---------------------------------------------------------------------------

func Test_InstallDependencies_Requirements(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	var capturedArgs exec.RunArgs
	mockContext.CommandRunner.When(
		func(args exec.RunArgs, command string) bool {
			capturedArgs = args
			return strings.Contains(
				command, "requirements.txt",
			)
		},
	).Respond(exec.NewRunResult(0, "", ""))

	err := cli.InstallDependencies(
		*mockContext.Context, tempDir, ".venv",
		"requirements.txt", nil,
	)
	require.NoError(t, err)
	require.NotEmpty(t, capturedArgs.Cwd)
}

func Test_InstallDependencies_Pyproject(t *testing.T) {
	tempDir := t.TempDir()
	mockContext := mocks.NewMockContext(context.Background())
	mockPythonInPath(mockContext)
	cli := NewCli(mockContext.CommandRunner)

	mockContext.CommandRunner.When(
		func(args exec.RunArgs, command string) bool {
			return strings.Contains(
				command, "pip install .",
			)
		},
	).Respond(exec.NewRunResult(0, "", ""))

	err := cli.InstallDependencies(
		*mockContext.Context, tempDir, ".venv",
		"pyproject.toml", nil,
	)
	require.NoError(t, err)
}

func Test_InstallDependencies_UnknownFile(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	cli := NewCli(mockContext.CommandRunner)

	// Capture log output to verify the default-case message.
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	err := cli.InstallDependencies(
		*mockContext.Context, "", "", "setup.cfg", nil,
	)
	require.NoError(t, err)
	require.Contains(t, buf.String(),
		"unsupported dependency file",
		"should log a skip message for unrecognized files",
	)
	require.Contains(t, buf.String(), "setup.cfg",
		"log message should include the file name",
	)
}
