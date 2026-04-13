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
// TS E2E test helpers
// ---------------------------------------------------------------------------

// newTSTestFixture creates a temp directory with the script
// file and optional package.json.
func newTSTestFixture(
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

// ---------------------------------------------------------------------------
// E2E TypeScript hook tests
// ---------------------------------------------------------------------------

func TestTsHook_AutoDetectFromExtension(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.ts")
	cwd := newTSTestFixture(t, scriptRel, false)

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
	var capturedArgs []string
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.ts")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		capturedCmd = args.Cmd
		capturedArgs = args.Args
		return exec.NewRunResult(0, "", ""), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.NoError(t, err)

	// Should invoke via npx tsx.
	assert.Equal(t, "npx", capturedCmd)
	require.GreaterOrEqual(t, len(capturedArgs), 4)
	assert.Equal(t, "--yes", capturedArgs[0])
	assert.Equal(t, "tsx", capturedArgs[1])
	assert.Equal(t, "--", capturedArgs[2])
	assert.Contains(t, capturedArgs[3], "predeploy.ts")

	hookCfg := hooksMap["predeploy"][0]
	assert.Equal(
		t, language.HookKindTypeScript, hookCfg.Kind,
	)
	assert.False(t, hookCfg.Kind.IsShell())
}

func TestTsHook_ExplicitKind(t *testing.T) {
	scriptRel := filepath.Join("hooks", "myscript")
	cwd := newTSTestFixture(t, scriptRel, false)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Kind: language.HookKindTypeScript,
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
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.NoError(t, err)
	require.True(
		t, executed,
		"explicit kind: ts should use TS executor",
	)
}

func TestTsHook_WithPackageJSON(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.ts")
	cwd := newTSTestFixture(t, scriptRel, true)

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
		return strings.Contains(command, "predeploy.ts")
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
	assert.Contains(t, callLog, "npm-install")
	assert.Contains(t, callLog, "execute")
}

func TestTsHook_NonZeroExitCode(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.ts")
	cwd := newTSTestFixture(t, scriptRel, false)

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
		return strings.Contains(command, "predeploy.ts")
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

func TestTsHook_ContinueOnError(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.ts")
	cwd := newTSTestFixture(t, scriptRel, false)

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
		return strings.Contains(command, "predeploy.ts")
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

	require.NoError(t, err)
}

func TestTsHook_ServiceLevel(t *testing.T) {
	serviceDir := filepath.Join("src", "api")
	scriptRel := filepath.Join(
		serviceDir, "hooks", "postdeploy.ts",
	)
	cwd := newTSTestFixture(t, scriptRel, false)

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
		return strings.Contains(command, "postdeploy.ts")
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
	expectedCwd := filepath.Join(
		cwd, serviceDir, "hooks",
	)
	assert.Equal(t, expectedCwd, capturedCwd)
}

func TestTsHook_NodeMissing(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.ts")
	cwd := newTSTestFixture(t, scriptRel, false)

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

	// Make node --version fail.
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
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)

	require.Error(t, err)

	var sugErr *errorhandler.ErrorWithSuggestion
	require.ErrorAs(t, err, &sugErr)
	assert.Contains(t, sugErr.Message, "Node.js is required")
}

func TestTsHook_EnvVarsPassthrough(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.ts")
	cwd := newTSTestFixture(t, scriptRel, false)

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
		return strings.Contains(command, "predeploy.ts")
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
	assert.Equal(t, "value", envMap["MY_SETTING"])
}

// ---------------------------------------------------------------------------
// Table-driven pipeline tests
// ---------------------------------------------------------------------------

func TestTsHook_ExecutionPipeline(t *testing.T) {
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
			scriptRel: filepath.Join("hooks", "hook.ts"),
			exitCode:  0,
			wantErr:   false,
		},
		{
			name:      "SuccessExplicitKind",
			scriptRel: filepath.Join("hooks", "run"),
			kind:      language.HookKindTypeScript,
			exitCode:  0,
			wantErr:   false,
		},
		{
			name:        "FailWithExitCode2",
			scriptRel:   filepath.Join("hooks", "fail.ts"),
			exitCode:    2,
			execErr:     fmt.Errorf("exit code 2"),
			wantErr:     true,
			errContains: "exit code: '2'",
		},
		{
			name:            "FailSuppressedByContinueOnError",
			scriptRel:       filepath.Join("hooks", "warn.ts"),
			continueOnError: true,
			exitCode:        1,
			execErr:         fmt.Errorf("exit code 1"),
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cwd := newTSTestFixture(
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

func TestTsHook_InlineScriptRejected(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	env := environment.NewWithValues(
		"test", map[string]string{},
	)

	hooksMap := map[string][]*HookConfig{
		"predeploy": {{
			Name: "predeploy",
			Kind: language.HookKindTypeScript,
			Run:  "console.log('hello')",
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

// TestTsHook_StdoutCapture verifies a TS hook that produces
// stdout completes without error.
func TestTsHook_StdoutCapture(t *testing.T) {
	scriptRel := filepath.Join("hooks", "predeploy.ts")
	cwd := newTSTestFixture(t, scriptRel, false)

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
		return strings.Contains(command, "predeploy.ts")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		// Simulate the script producing stdout.
		return exec.NewRunResult(
			0, "Hello from TS hook!", "",
		), nil
	})

	runner := buildRunner(
		t, mockCtx, cwd, hooksMap, env,
	)
	err := runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)
	require.NoError(t, err)
}

// TestTsHook_ShellHookUnaffected verifies that a Bash (.sh)
// hook runs through the shell executor even when TS hooks are
// present in the same configuration.
func TestTsHook_ShellHookUnaffected(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	// Create both shell and TS scripts.
	shScript := filepath.Join(cwd, "hooks", "prebuild.sh")
	tsScript := filepath.Join(cwd, "hooks", "predeploy.ts")
	for _, p := range []string{shScript, tsScript} {
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
				"hooks", "predeploy.ts",
			),
		}},
	}

	mockCtx := mocks.NewMockContext(context.Background())
	registerHookExecutors(mockCtx)
	stubNodeVersionCheck(mockCtx)

	shellRan := false
	tsRan := false

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

	// TS hook mock.
	mockCtx.CommandRunner.When(func(
		args exec.RunArgs, command string,
	) bool {
		return strings.Contains(command, "predeploy.ts")
	}).RespondFn(func(
		args exec.RunArgs,
	) (exec.RunResult, error) {
		tsRan = true
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

	// Run the TS hook.
	err = runner.RunHooks(
		*mockCtx.Context, HookTypePre, nil, "deploy",
	)
	require.NoError(t, err)
	require.True(
		t, tsRan,
		"TS hook should execute via non-shell pipeline",
	)
}

// TestTsHook_ExplicitDirOverridesCwd verifies that the Dir
// field in HookConfig overrides the default working directory
// for TS hook execution.
func TestTsHook_ExplicitDirOverridesCwd(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	// Create script and a custom directory.
	scriptDir := filepath.Join(cwd, "hooks")
	require.NoError(t, os.MkdirAll(
		scriptDir, osutil.PermissionDirectory,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(scriptDir, "predeploy.ts"),
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
			Run:  filepath.Join("hooks", "predeploy.ts"),
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
		return strings.Contains(command, "predeploy.ts")
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

// TestTsHook_ProjectLevel verifies a TS hook registered at the
// project level (preprovision) executes through the hook
// executor pipeline.
func TestTsHook_ProjectLevel(t *testing.T) {
	scriptRel := filepath.Join("hooks", "preprovision.ts")
	cwd := newTSTestFixture(t, scriptRel, false)

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
		return strings.Contains(command, "preprovision.ts")
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
	require.True(t, executed, "project-level TS hook should execute")
}
