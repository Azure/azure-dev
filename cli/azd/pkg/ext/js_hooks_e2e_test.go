// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/errorhandler"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// JS E2E test helpers
// ---------------------------------------------------------------------------

// newJSTestFixture creates a temp directory with the script
// file and optional package.json.
func newJSTestFixture(
	t *testing.T,
	scriptRelPath string,
	withPackageJSON bool,
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

	if withPackageJSON {
		pkgPath := filepath.Join(
			filepath.Dir(absScript), "package.json",
		)
		require.NoError(t, os.WriteFile(
			pkgPath,
			[]byte(`{"name":"test","version":"1.0.0"}`),
			osutil.PermissionFile,
		))
	}

	return cwd
}

// stubNodeVersionCheck registers a mock for the Node.js
// --version call that always succeeds.
func stubNodeVersionCheck(
	mockCtx *mocks.MockContext,
) {
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "--version") &&
			strings.Contains(command, "node")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.NewRunResult(
			0, "v20.11.0", "",
		), nil
	})
}

// stubNpmInstall registers a mock for npm install.
func stubNpmInstall(
	mockCtx *mocks.MockContext,
	callLog *[]string,
) {
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "npm") &&
			strings.Contains(command, "install")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		if callLog != nil {
			*callLog = append(*callLog, "npm-install")
		}
		return exec.NewRunResult(0, "", ""), nil
	})
}

// ---------------------------------------------------------------------------
// E2E JavaScript hook tests
// ---------------------------------------------------------------------------

func TestJsHook_AutoDetectFromExtension(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.js")
	cwd := newJSTestFixture(t, scriptRel, false)

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
	stubNodeVersionCheck(mockCtx)

	var executedScript string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
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
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.NoError(t, err)
	assert.Contains(
		t, executedScript, "predeploy.js",
		"auto-detected JS hook should execute the .js script",
	)

	hookCfg := hooksMap["predeploy"][0]
	assert.Equal(
		t, language.HookKindJavaScript, hookCfg.Kind,
	)
	assert.False(t, hookCfg.Kind.IsShell())
}

func TestJsHook_ExplicitKind(t *testing.T) {
	scriptRel := filepath.Join("hooks", "myscript")
	cwd := newJSTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Kind: language.HookKindJavaScript,
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubNodeVersionCheck(mockCtx)

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
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.NoError(t, err)
	require.True(
		t, executed,
		"explicit kind: js should use JS executor",
	)
}

func TestJsHook_WithPackageJSON(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.js")
	cwd := newJSTestFixture(t, scriptRel, true)

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
	stubNodeVersionCheck(mockCtx)

	callLog := []string{}
	stubNpmInstall(mockCtx, &callLog)

	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
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
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.NoError(t, err)
	assert.Contains(t, callLog, "npm-install")
	assert.Contains(t, callLog, "execute")
}

func TestJsHook_NodeBinaryResolution(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.js")
	cwd := newJSTestFixture(t, scriptRel, false)

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
	stubNodeVersionCheck(mockCtx)

	var capturedCmd string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
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
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.NoError(t, err)
	assert.Equal(t, "node", capturedCmd,
		"JS executor should use 'node' command")
}

func TestJsHook_NonZeroExitCode(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.js")
	cwd := newJSTestFixture(t, scriptRel, false)

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
	stubNodeVersionCheck(mockCtx)

	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
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
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "'predeploy' hook failed")
	assert.Contains(t, err.Error(), "exit code: '1'")
}

func TestJsHook_ContinueOnError(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.js")
	cwd := newJSTestFixture(t, scriptRel, false)

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
	stubNodeVersionCheck(mockCtx)

	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
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
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.NoError(t, err)
}

func TestJsHook_ServiceLevel(t *testing.T) {
	serviceDir := filepath.Join("src", "api")
	scriptRel := filepath.Join(
		serviceDir, "hooks", "postdeploy.js",
	)
	cwd := newJSTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"postdeploy": {{
			Name: "postdeploy",
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubNodeVersionCheck(mockCtx)

	var capturedCwd string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "postdeploy.js")
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
		*mockCtx.Context, HookTypePost, "project", nil, "deploy",
	)

	require.NoError(t, err)
	expectedCwd := filepath.Join(
		cwd, serviceDir, "hooks",
	)
	assert.Equal(t, expectedCwd, capturedCwd)
}

func TestJsHook_EnvVarsPassthrough(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.js")
	cwd := newJSTestFixture(t, scriptRel, false)

	env := environment.NewWithValues("test", map[string]string{
		"AZURE_ENV_NAME": "dev",
		"MY_SETTING":     "value",
	})

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Run:  scriptRel,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubNodeVersionCheck(mockCtx)

	var capturedEnv []string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
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
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.NoError(t, err)
	require.NotEmpty(t, capturedEnv)

	envMap := envSliceToMap(capturedEnv)
	assert.Equal(t, "dev", envMap["AZURE_ENV_NAME"])
	assert.Equal(t, "value", envMap["MY_SETTING"])
}

// TestJsHook_NodeMissing verifies that an appropriate error is
// returned when Node.js is not installed.
func TestJsHook_NodeMissing(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.js")
	cwd := newJSTestFixture(t, scriptRel, false)

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

	// Register all executors but make node --version fail.
	registerHookExecutors(mockCtx)

	// Override node --version to fail.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "node") &&
			strings.Contains(command, "--version")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.NewRunResult(
			1, "", "not found",
		), fmt.Errorf("node not found")
	})

	// Also make npm/node ToolInPath check fail.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "npm") &&
			strings.Contains(command, "--version")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		return exec.NewRunResult(
			1, "", "not found",
		), fmt.Errorf("npm not found")
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.Error(t, err)

	var sugErr *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, err, &sugErr)
	assert.Contains(t, sugErr.Message, "Node.js is required")
}

// ---------------------------------------------------------------------------
// Table-driven pipeline tests
// ---------------------------------------------------------------------------

func TestJsHook_ExecutionPipeline(t *testing.T) {
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
			scriptRel: filepath.Join("hooks", "hook.js"),
			exitCode:  0,
			wantErr:   false,
		},
		{
			name:      "SuccessExplicitKind",
			scriptRel: filepath.Join("hooks", "run"),
			kind:      language.HookKindJavaScript,
			exitCode:  0,
			wantErr:   false,
		},
		{
			name:        "FailWithExitCode2",
			scriptRel:   filepath.Join("hooks", "fail.js"),
			exitCode:    2,
			execErr:     fmt.Errorf("exit code 2"),
			wantErr:     true,
			errContains: "exit code: '2'",
		},
		{
			name:            "FailSuppressedByContinueOnError",
			scriptRel:       filepath.Join("hooks", "warn.js"),
			continueOnError: true,
			exitCode:        1,
			execErr:         fmt.Errorf("exit code 1"),
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := newJSTestFixture(
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
			stubNodeVersionCheck(mockCtx)

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
				HookTypePre, "project", nil, "deploy",
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

func TestJsHook_InlineScriptRejected(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Kind: language.HookKindJavaScript,
			Run:  "console.log('hello')",
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.Error(t, err)
	assert.Contains(
		t, err.Error(), "inline scripts are not supported",
	)
}

// TestJsHook_StdoutCapture verifies a JS hook that produces
// stdout completes without error.
func TestJsHook_StdoutCapture(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.js")
	cwd := newJSTestFixture(t, scriptRel, false)

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
	stubNodeVersionCheck(mockCtx)

	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		// Simulate the script producing stdout.
		return exec.NewRunResult(
			0, "Hello from JS hook!", "",
		), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)
	require.NoError(t, err)
}

// TestJsHook_ShellHookUnaffected verifies that a Bash (.sh)
// hook runs through the shell executor even when JS hooks are
// present in the same configuration.
func TestJsHook_ShellHookUnaffected(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	// Create both shell and JS scripts.
	shScript := filepath.Join(cwd, "hooks", "prebuild.sh")
	jsScript := filepath.Join(cwd, "hooks", "predeploy.js")
	for _, p := range []string{shScript, jsScript} {
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
				"hooks", "predeploy.js",
			),
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubNodeVersionCheck(mockCtx)

	shellRan := false
	jsRan := false

	// Shell hook mock.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "prebuild.sh")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		shellRan = true
		require.Contains(
			t, args.Args[0], "prebuild.sh",
		)
		return exec.NewRunResult(0, "", ""), nil
	})

	// JS hook mock.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		jsRan = true
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)

	// Run the shell hook.
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, "project", nil, "build",
	)
	require.NoError(t, err)
	require.True(
		t, shellRan,
		"shell hook should execute via shell pipeline",
	)

	// Run the JS hook.
	err = runner.RunHooks(
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)
	require.NoError(t, err)
	require.True(
		t, jsRan,
		"JS hook should execute via non-shell pipeline",
	)
}

// TestJsHook_ExplicitDirOverridesCwd verifies that the Dir
// field in HookConfig overrides the default working directory
// for JS hook execution.
func TestJsHook_ExplicitDirOverridesCwd(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	// Create script and a custom directory.
	scriptDir := filepath.Join(cwd, "hooks")
	require.NoError(t, os.MkdirAll(
		scriptDir, osutil.PermissionDirectory,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(scriptDir, "predeploy.js"),
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
			Run:  filepath.Join("hooks", "predeploy.js"),
			Dir:  customDir,
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubNodeVersionCheck(mockCtx)

	var capturedCwd string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.js")
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
		*mockCtx.Context, HookTypePre, "project", nil, "deploy",
	)

	require.NoError(t, err)
	assert.Equal(
		t, customDir, capturedCwd,
		"Dir should override the default working directory",
	)
}

// TestJsHook_ProjectLevel verifies a JS hook registered at the
// project level (preprovision) executes through the hook
// executor pipeline.
func TestJsHook_ProjectLevel(t *testing.T) {
	scriptRel := filepath.Join("hooks", "preprovision.js")
	cwd := newJSTestFixture(t, scriptRel, false)

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
	stubNodeVersionCheck(mockCtx)

	executed := false
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "preprovision.js")
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
		*mockCtx.Context, HookTypePre, "project", nil, "provision",
	)

	require.NoError(t, err)
	require.True(t, executed, "project-level JS hook should execute")
}
