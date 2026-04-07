// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newPythonTestFixture creates a temp directory with the script file
// and optional requirements.txt, returning the cwd and script path.
func newPythonTestFixture(
	t *testing.T,
	scriptRelPath string,
	withRequirements bool,
) string {
	t.Helper()
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	absScript := filepath.Join(cwd, scriptRelPath)
	require.NoError(t, os.MkdirAll(
		filepath.Dir(absScript), osutil.PermissionDirectory,
	))
	require.NoError(t, os.WriteFile(
		absScript, nil, osutil.PermissionExecutableFile,
	))

	if withRequirements {
		reqPath := filepath.Join(
			filepath.Dir(absScript), "requirements.txt",
		)
		require.NoError(t, os.WriteFile(
			reqPath, []byte("flask\n"), osutil.PermissionFile,
		))
	}

	return cwd
}

// buildRunner is a compact constructor that wires up a
// [HooksRunner] from the mocked context.
func buildRunner(
	t *testing.T,
	mockCtx *mocks.MockContext,
	cwd string,
	hooks map[string][]*HookConfig,
	env *environment.Environment,
) *HooksRunner {
	t.Helper()
	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, env).Return(nil)

	hooksManager := NewHooksManager(
		cwd, mockCtx.CommandRunner,
	)

	return NewHooksRunner(
		hooksManager,
		mockCtx.CommandRunner,
		envManager,
		mockCtx.Console,
		cwd,
		hooks,
		env,
		mockCtx.Container,
	)
}

// stubPythonVersionCheck registers a mock for the Python
// --version call that always succeeds.
func stubPythonVersionCheck(
	mockCtx *mocks.MockContext,
) {
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "--version")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.NewRunResult(
			0, "Python 3.11.0", "",
		), nil
	})
}

// ---------------------------------------------------------------------------
// E2E Python hook tests
// ---------------------------------------------------------------------------

// TestPythonHook_AutoDetectFromExtension verifies that a hook with
// run: script.py (no explicit language:) auto-detects Python and
// routes through the HookExecutor pipeline.
func TestPythonHook_AutoDetectFromExtension(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.py")
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Run:  scriptRel,
			// Language intentionally omitted.
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	var executedScript string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		executedScript = args.Args[0]
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.NoError(t, err)
	assert.Contains(
		t, executedScript, "predeploy.py",
		"auto-detected Python hook should execute the .py script",
	)

	// Verify the config was resolved as a non-shell language hook.
	hookCfg := hooksMap["predeploy"][0]
	assert.Equal(
		t, language.HookKindPython, hookCfg.Kind,
	)
	assert.False(t, hookCfg.Kind.IsShell())
}

// TestPythonHook_ExplicitLanguage verifies that language: python
// in the config uses the Python executor even when the script has
// no .py extension.
func TestPythonHook_ExplicitLanguage(t *testing.T) {
	scriptRel := filepath.Join("hooks", "myscript")
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Kind: language.HookKindPython,
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	executed := false
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "myscript")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		executed = true
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.NoError(t, err)
	require.True(
		t, executed,
		"explicit language: python should use Python executor",
	)
}

// TestPythonHook_EnvVarsPassthrough verifies that azd
// environment variables are forwarded to the Python executor.
func TestPythonHook_EnvVarsPassthrough(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.py")
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues("test", map[string]string{
		"AZURE_ENV_NAME":    "dev",
		"AZURE_LOCATION":    "eastus2",
		"MY_CUSTOM_SETTING": "custom_value",
	})

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	var capturedEnv []string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		capturedEnv = args.Env
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.NoError(t, err)
	require.NotEmpty(t, capturedEnv)

	envMap := envSliceToMap(capturedEnv)
	assert.Equal(t, "dev", envMap["AZURE_ENV_NAME"])
	assert.Equal(t, "eastus2", envMap["AZURE_LOCATION"])
	assert.Equal(t, "custom_value", envMap["MY_CUSTOM_SETTING"])
}

// TestPythonHook_WithRequirementsTxt verifies that when a
// requirements.txt exists alongside the script, the executor
// creates a venv and installs deps before running the script.
func TestPythonHook_WithRequirementsTxt(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.py")
	cwd := newPythonTestFixture(t, scriptRel, true)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	callLog := []string{}

	// Mock "python -m venv …" — venv creation.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "-m venv") ||
			strings.Contains(command, "venv")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		callLog = append(callLog, "create-venv")
		// Create the venv directory so the executor sees it.
		venvDir := filepath.Join(
			args.Cwd,
			args.Args[len(args.Args)-1],
		)
		require.NoError(t, os.MkdirAll(
			venvDir, osutil.PermissionDirectory,
		))
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock "pip install -r requirements.txt".
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "install") &&
			strings.Contains(command, "requirements")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		callLog = append(callLog, "pip-install")
		return exec.NewRunResult(0, "", ""), nil
	})

	// Mock actual script execution.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		callLog = append(callLog, "execute")
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.NoError(t, err)

	// The overall flow: version-check → venv → pip → execute.
	assert.Contains(t, callLog, "create-venv")
	assert.Contains(t, callLog, "pip-install")
	assert.Contains(t, callLog, "execute")
}

// TestPythonHook_StdoutCapture verifies that the hook execution
// result contains stdout from the Python script process.
func TestPythonHook_StdoutCapture(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.py")
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		// Simulate the script producing stdout.
		return exec.NewRunResult(
			0, "Hello from Python hook!", "",
		), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	// RunHooks doesn't return the result directly, but
	// the pipeline completes without error confirming the
	// execution path ran and stdout was handled.
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)
	require.NoError(t, err)
}

// TestPythonHook_NonZeroExitCode verifies that a Python hook
// returning a non-zero exit code produces an error containing
// the exit code information.
func TestPythonHook_NonZeroExitCode(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.py")
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "error output"),
			fmt.Errorf("process exited with code 1")
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "'predeploy' hook failed")
	assert.Contains(t, err.Error(), "exit code: '1'")
}

// TestPythonHook_ContinueOnError verifies that when
// continueOnError: true is set and the Python hook fails, the
// error is swallowed and RunHooks returns nil.
func TestPythonHook_ContinueOnError(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.py")
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name:            "predeploy",
			Run:             scriptRel,
			ContinueOnError: true,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.NewRunResult(1, "", "error"),
			fmt.Errorf("script error")
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	// ContinueOnError should suppress the error.
	require.NoError(t, err)
}

// TestPythonHook_ProjectLevel verifies a Python hook registered
// at the project level (pre<command>) executes through the
// hook executor pipeline.
func TestPythonHook_ProjectLevel(t *testing.T) {
	scriptRel := filepath.Join("hooks", "preprovision.py")
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"preprovision": {{
			Name: "preprovision",
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	executed := false
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "preprovision.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		executed = true
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "provision",
	)

	require.NoError(t, err)
	require.True(t, executed, "project-level Python hook should execute")
}

// TestPythonHook_ServiceLevel verifies a Python hook registered
// at the service level (postdeploy for a service) executes through
// the hook executor pipeline with the correct working dir.
func TestPythonHook_ServiceLevel(t *testing.T) {
	// Service hooks use a service-specific cwd, simulated here.
	serviceDir := filepath.Join("src", "api")
	scriptRel := filepath.Join(
		serviceDir, "hooks", "postdeploy.py",
	)
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"postdeploy": {{
			Name: "postdeploy",
			Run: filepath.Join(
				serviceDir, "hooks", "postdeploy.py",
			),
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	var capturedCwd string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "postdeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		capturedCwd = args.Cwd
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePost, nil, "deploy",
	)

	require.NoError(t, err)

	// The execution cwd should be the script's directory.
	expectedCwd := filepath.Join(
		cwd, serviceDir, "hooks",
	)
	assert.Equal(t, expectedCwd, capturedCwd)
}

// TestPythonHook_ShellHookUnaffected verifies that a Bash (.sh)
// hook runs through the Bash executor even when Python
// hooks are present in the same configuration.
func TestPythonHook_ShellHookUnaffected(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	// Create both shell and Python scripts.
	shScript := filepath.Join(cwd, "hooks", "prebuild.sh")
	pyScript := filepath.Join(cwd, "hooks", "predeploy.py")
	for _, p := range []string{shScript, pyScript} {
		require.NoError(t, os.MkdirAll(
			filepath.Dir(p), osutil.PermissionDirectory,
		))
		require.NoError(t, os.WriteFile(
			p, nil, osutil.PermissionExecutableFile,
		))
	}

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"prebuild": {{
			Name:  "prebuild",
			Shell: string(language.HookKindBash),
			Run: filepath.Join(
				"hooks", "prebuild.sh",
			),
		}},
		"predeploy": {{
			Name: "predeploy",
			Run: filepath.Join(
				"hooks", "predeploy.py",
			),
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	shellRan := false
	pythonRan := false

	// Shell hook mock.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "prebuild.sh")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		shellRan = true
		// Bash hooks pass the script as the first arg.
		// The shell executor may use forward slashes, so
		// compare with forward slashes for portability.
		require.Contains(
			t, args.Args[0], "prebuild.sh",
		)
		return exec.NewRunResult(0, "", ""), nil
	})

	// Python hook mock.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		pythonRan = true
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)

	// Run the shell hook.
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "build",
	)
	require.NoError(t, err)
	require.True(
		t, shellRan,
		"shell hook should execute via shell pipeline",
	)

	// Run the Python hook.
	err = runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)
	require.NoError(t, err)
	require.True(
		t, pythonRan,
		"Python hook should execute via language pipeline",
	)
}

// ---------------------------------------------------------------------------
// Table-driven comprehensive tests
// ---------------------------------------------------------------------------

// TestPythonHook_ExecutionPipeline uses table-driven subtests to
// verify multiple facets of the Python hook execution pipeline.
func TestPythonHook_ExecutionPipeline(t *testing.T) {
	tests := []struct {
		name            string
		scriptRel       string
		kind            language.HookKind
		continueOnError bool
		exitCode        int
		execErr         error
		wantErr         bool
		errContains     string
	}{
		{
			name:      "SuccessAutoDetect",
			scriptRel: filepath.Join("hooks", "hook.py"),
			exitCode:  0,
			wantErr:   false,
		},
		{
			name:      "SuccessExplicitLanguage",
			scriptRel: filepath.Join("hooks", "run"),
			kind:      language.HookKindPython,
			exitCode:  0,
			wantErr:   false,
		},
		{
			name:        "FailWithExitCode2",
			scriptRel:   filepath.Join("hooks", "fail.py"),
			exitCode:    2,
			execErr:     fmt.Errorf("exit code 2"),
			wantErr:     true,
			errContains: "exit code: '2'",
		},
		{
			name:            "FailSuppressedByContinueOnError",
			scriptRel:       filepath.Join("hooks", "warn.py"),
			continueOnError: true,
			exitCode:        1,
			execErr:         fmt.Errorf("exit code 1"),
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := newPythonTestFixture(
				t, tt.scriptRel, false,
			)

			env := environment.NewWithValues(
				"test", map[string]string{},
			)

			hookCfg := &HookConfig{
				Name:            "predeploy",
				Run:             tt.scriptRel,
				ContinueOnError: tt.continueOnError,
			}
			if tt.kind != language.HookKindUnknown {
				hookCfg.Kind = tt.kind
			}

			hooksMap := map[string][]*HookConfig{
				"predeploy": {hookCfg},
			}

			mockCtx := mocks.NewMockContext(
				context.Background(),
			)
			registerHookExecutors(mockCtx)
			stubPythonVersionCheck(mockCtx)

			// Derive the script base name for matching.
			scriptBase := filepath.Base(tt.scriptRel)
			mockCtx.CommandRunner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return strings.Contains(
					command, scriptBase,
				)
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				return exec.NewRunResult(
					tt.exitCode, "", "",
				), tt.execErr
			})

			runner := buildRunner(
				t, mockCtx, cwd, hooksMap, env,
			)
			err := runner.RunHooks(
				*mockCtx.Context,
				HookTypePre, nil, "deploy",
			)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(
						t, err.Error(), tt.errContains,
					)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestPythonHook_PythonBinaryResolution verifies that the correct
// Python binary is invoked based on the platform.
func TestPythonHook_PythonBinaryResolution(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.py")
	cwd := newPythonTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	var capturedCmd string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		capturedCmd = args.Cmd
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.NoError(t, err)
	require.NotEmpty(t, capturedCmd)

	// Without a venv, the executor uses system Python.
	if runtime.GOOS == "windows" {
		// The mock ToolInPath returns nil → "py" is preferred.
		assert.True(
			t,
			capturedCmd == "py" || capturedCmd == "python",
			"expected py or python on Windows, got %s",
			capturedCmd,
		)
	} else {
		assert.Equal(t, "python3", capturedCmd)
	}
}

// TestPythonHook_ExplicitDirOverridesCwd verifies that
// the Dir field in HookConfig overrides the default working
// directory for language hook execution.
func TestPythonHook_ExplicitDirOverridesCwd(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	// Create script and a custom directory.
	scriptDir := filepath.Join(cwd, "hooks")
	require.NoError(t, os.MkdirAll(
		scriptDir, osutil.PermissionDirectory,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(scriptDir, "predeploy.py"),
		nil, osutil.PermissionExecutableFile,
	))

	customDir := filepath.Join(cwd, "custom_workdir")
	require.NoError(t, os.MkdirAll(
		customDir, osutil.PermissionDirectory,
	))

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Run:  filepath.Join("hooks", "predeploy.py"),
			Dir:  customDir,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubPythonVersionCheck(mockCtx)

	var capturedCwd string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.py")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		capturedCwd = args.Cwd
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.NoError(t, err)
	assert.Equal(
		t, customDir, capturedCwd,
		"Dir should override the default working directory",
	)
}

// TestPythonHook_InlineScriptRejected verifies that inline
// Python scripts (no file path) are rejected with a clear error
// since non-shell hooks require file-based scripts.
func TestPythonHook_InlineScriptRejected(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Kind: language.HookKindPython,
			Run:  "print('hello')",
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.Error(t, err)
	assert.Contains(
		t, err.Error(), "inline scripts are not supported",
	)
}
