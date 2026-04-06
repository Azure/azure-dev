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
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bash"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/language"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/powershell"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// registerHookExecutors registers all hook executors as named
// transients in the mock container so that IoC resolution works
// in tests.
func registerHookExecutors(mockCtx *mocks.MockContext) {
	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguageBash), bash.NewExecutor,
	)
	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguagePowerShell), powershell.NewExecutor,
	)

	// Register python.Cli (needed by NewPythonExecutor IoC constructor).
	mockCtx.Container.MustRegisterSingleton(python.NewCli)

	mockCtx.Container.MustRegisterNamedTransient(
		string(language.ScriptLanguagePython), language.NewPythonExecutor,
	)
}

func Test_Hooks_Execute(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)
	scriptsDir := filepath.Join(cwd, "scripts")

	env := environment.NewWithValues(
		"test",
		map[string]string{
			"a": "apple",
			"b": "banana",
		},
	)

	hooksMap := map[string][]*HookConfig{
		"preinline": {
			{
				Shell: "sh",
				Run:   "echo 'Hello'",
			},
		},
		"precommand": {
			{
				Shell: "sh",
				Run:   "scripts/precommand.sh",
			},
		},
		"postcommand": {{
			Shell: "sh",
			Run:   "scripts/postcommand.sh",
		},
		},
		"preinteractive": {
			{
				Shell:       "sh",
				Run:         "scripts/preinteractive.sh",
				Interactive: true,
			},
		},
	}

	ensureScriptsExist(t, hooksMap)

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, env).Return(nil)

	t.Run("PreHook", func(t *testing.T) {
		ranPreHook := false
		ranPostHook := false

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "precommand.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPreHook = true
			require.Equal(t, filepath.ToSlash(
				filepath.Join(scriptsDir, "precommand.sh"),
			), args.Args[0])
			require.Equal(t, scriptsDir, args.Cwd)
			require.ElementsMatch(t, env.Environ(), args.Env)
			require.Equal(t, false, args.Interactive)

			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)
		err := runner.RunHooks(*mockContext.Context, HookTypePre, nil, "command")

		require.True(t, ranPreHook)
		require.False(t, ranPostHook)
		require.NoError(t, err)
	})

	t.Run("PostHook", func(t *testing.T) {
		ranPreHook := false
		ranPostHook := false

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "postcommand.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPostHook = true
			require.Equal(t, filepath.ToSlash(
				filepath.Join(scriptsDir, "postcommand.sh"),
			), args.Args[0])
			require.Equal(t, scriptsDir, args.Cwd)
			require.ElementsMatch(t, env.Environ(), args.Env)
			require.Equal(t, false, args.Interactive)

			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)
		err := runner.RunHooks(*mockContext.Context, HookTypePost, nil, "command")

		require.False(t, ranPreHook)
		require.True(t, ranPostHook)
		require.NoError(t, err)
	})

	t.Run("Interactive", func(t *testing.T) {
		ranPreHook := false
		ranPostHook := false

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "preinteractive.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPostHook = true
			require.Equal(t, filepath.ToSlash(
				filepath.Join(scriptsDir, "preinteractive.sh"),
			), args.Args[0])
			require.Equal(t, scriptsDir, args.Cwd)
			require.ElementsMatch(t, env.Environ(), args.Env)
			require.Equal(t, true, args.Interactive)

			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)
		err := runner.RunHooks(*mockContext.Context, HookTypePre, nil, "interactive")

		require.False(t, ranPreHook)
		require.True(t, ranPostHook)
		require.NoError(t, err)
	})

	t.Run("Inline Hook", func(t *testing.T) {
		ranPreHook := false
		ranPostHook := false

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "preinline")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPostHook = true

			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)
		err := runner.RunHooks(*mockContext.Context, HookTypePre, nil, "inline")

		require.False(t, ranPreHook)
		require.True(t, ranPostHook)
		require.NoError(t, err)
	})

	t.Run("InvokeAction", func(t *testing.T) {
		ranPreHook := false
		ranPostHook := false
		ranAction := false

		hookLog := []string{}

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "precommand.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPreHook = true
			hookLog = append(hookLog, "pre")
			require.Equal(t, filepath.ToSlash(
				filepath.Join(scriptsDir, "precommand.sh"),
			), args.Args[0])

			return exec.NewRunResult(0, "", ""), nil
		})

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "postcommand.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPostHook = true
			hookLog = append(hookLog, "post")
			require.Equal(t, filepath.ToSlash(
				filepath.Join(scriptsDir, "postcommand.sh"),
			), args.Args[0])

			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)
		err := runner.Invoke(*mockContext.Context, []string{"command"}, func() error {
			ranAction = true
			hookLog = append(hookLog, "action")
			return nil
		})

		require.True(t, ranPreHook)
		require.True(t, ranAction)
		require.True(t, ranPostHook)

		// Validates the hooks and action are run in the correct order
		require.Equal(t, []string{
			"pre",
			"action",
			"post",
		}, hookLog)

		require.NoError(t, err)
	})
}

// Test_Hooks_Validation verifies that hook configuration validation
// works correctly for all supported script types through the unified
// execHook path.
func Test_Hooks_Validation(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	env := environment.NewWithValues(
		"test",
		map[string]string{
			"a": "apple",
			"b": "banana",
		},
	)

	// Create script files on disk for validation.
	require.NoError(t, os.MkdirAll(filepath.Join(cwd, "scripts"), osutil.PermissionDirectory))
	require.NoError(t, os.WriteFile(
		filepath.Join(cwd, "scripts", "script.sh"), nil, osutil.PermissionExecutableFile,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(cwd, "scripts", "script.ps1"), nil, osutil.PermissionExecutableFile,
	))

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Reload", mock.Anything, env).Return(nil)

	t.Run("BashHookExecutes", func(t *testing.T) {
		hooksMap := map[string][]*HookConfig{
			"predeploy": {{
				Name:  "predeploy",
				Shell: "sh",
				Run:   "scripts/script.sh",
			}},
		}

		shellRan := false
		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "script.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			shellRan = true
			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager, mockContext.CommandRunner, envManager,
			mockContext.Console, cwd, hooksMap, env, mockContext.Container,
		)

		err := runner.RunHooks(*mockContext.Context, HookTypePre, nil, "deploy")
		require.NoError(t, err)
		require.True(t, shellRan)
	})

	t.Run("PowershellHookExecutes", func(t *testing.T) {
		hooksMap := map[string][]*HookConfig{
			"predeploy": {{
				Name: "predeploy",
				Run:  "scripts/script.ps1",
			}},
		}

		shellRan := false
		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.MockToolInPath("pwsh", nil)

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "script.ps1")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			shellRan = true
			require.Equal(t, "pwsh", args.Cmd)
			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager, mockContext.CommandRunner, envManager,
			mockContext.Console, cwd, hooksMap, env, mockContext.Container,
		)

		err := runner.RunHooks(*mockContext.Context, HookTypePre, nil, "deploy")
		require.NoError(t, err)
		require.True(t, shellRan)
	})

	t.Run("InlineBashHookExecutes", func(t *testing.T) {
		hooksMap := map[string][]*HookConfig{
			"preinline": {{
				Name:  "preinline",
				Shell: "sh",
				Run:   "echo 'Hello'",
			}},
		}

		inlineRan := false
		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "preinline")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			inlineRan = true
			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager, mockContext.CommandRunner, envManager,
			mockContext.Console, cwd, hooksMap, env, mockContext.Container,
		)

		err := runner.RunHooks(*mockContext.Context, HookTypePre, nil, "inline")
		require.NoError(t, err)
		require.True(t, inlineRan)
	})

	t.Run("MissingRunReturnsError", func(t *testing.T) {
		hooksMap := map[string][]*HookConfig{
			"predeploy": {{
				Name:  "predeploy",
				Shell: "sh",
			}},
		}

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager, mockContext.CommandRunner, envManager,
			mockContext.Console, cwd, hooksMap, env, mockContext.Container,
		)

		err := runner.RunHooks(*mockContext.Context, HookTypePre, nil, "deploy")
		require.Error(t, err)
		require.ErrorIs(t, err, ErrRunRequired)
	})
}

// Test_ExecHook_LanguageHooks verifies the integration between
// [HooksRunner] and [tools.HookExecutor] for non-shell hooks.
func Test_ExecHook_LanguageHooks(t *testing.T) {
	t.Run("PythonLanguageHook", func(t *testing.T) {
		cwd := t.TempDir()
		ostest.Chdir(t, cwd)

		env := environment.NewWithValues("test", map[string]string{
			"FOO": "bar",
		})

		// Create a .py script file on disk so validate() sees it.
		scriptDir := filepath.Join(cwd, "hooks")
		require.NoError(t, os.MkdirAll(scriptDir, osutil.PermissionDirectory))
		scriptFile := filepath.Join(scriptDir, "predeploy.py")
		require.NoError(t, os.WriteFile(scriptFile, nil, osutil.PermissionExecutableFile))

		hooksMap := map[string][]*HookConfig{
			"predeploy": {
				{
					Name: "predeploy",
					Run:  filepath.Join("hooks", "predeploy.py"),
				},
			},
		}

		envManager := &mockenv.MockEnvManager{}
		envManager.On("Reload", mock.Anything, env).Return(nil)

		prepareRan := false
		executeRan := false

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)

		// Mock the Python version check issued by python.Cli.CheckInstalled
		// via tools.ExecuteCommand → commandRunner.Run.
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "--version")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			prepareRan = true
			return exec.NewRunResult(0, "Python 3.11.0", ""), nil
		})

		// Mock the actual Python script execution.
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "predeploy.py")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			executeRan = true
			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)

		err := runner.RunHooks(
			*mockContext.Context, HookTypePre, nil, "deploy",
		)

		require.NoError(t, err)
		require.True(t, prepareRan, "Prepare (version check) should have run")
		require.True(t, executeRan, "Execute should have run the .py script")
	})

	t.Run("ShellHookAbsolutePath", func(t *testing.T) {
		cwd := t.TempDir()
		ostest.Chdir(t, cwd)

		env := environment.NewWithValues("test", map[string]string{})

		hooksMap := map[string][]*HookConfig{
			"predeploy": {
				{
					Name:  "predeploy",
					Shell: "sh",
					Run:   "scripts/predeploy.sh",
				},
			},
		}
		ensureScriptsExist(t, hooksMap)

		envManager := &mockenv.MockEnvManager{}
		envManager.On("Reload", mock.Anything, env).Return(nil)

		shellRan := false

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "predeploy.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			shellRan = true
			require.Equal(t, filepath.ToSlash(
				filepath.Join(cwd, "scripts", "predeploy.sh"),
			), args.Args[0])
			require.Equal(t, filepath.Join(cwd, "scripts"), args.Cwd)
			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)

		err := runner.RunHooks(
			*mockContext.Context, HookTypePre, nil, "deploy",
		)

		require.NoError(t, err)
		require.True(t, shellRan, "Bash executor should use absolute path for .sh hooks")
	})

	t.Run("LanguageHookPrepareFailure", func(t *testing.T) {
		cwd := t.TempDir()
		ostest.Chdir(t, cwd)

		env := environment.NewWithValues("test", map[string]string{})

		scriptDir := filepath.Join(cwd, "hooks")
		require.NoError(t, os.MkdirAll(scriptDir, osutil.PermissionDirectory))
		scriptFile := filepath.Join(scriptDir, "predeploy.py")
		require.NoError(t, os.WriteFile(
			scriptFile, nil, osutil.PermissionExecutableFile,
		))

		hooksMap := map[string][]*HookConfig{
			"predeploy": {
				{
					Name: "predeploy",
					Run:  filepath.Join("hooks", "predeploy.py"),
				},
			},
		}

		envManager := &mockenv.MockEnvManager{}
		envManager.On("Reload", mock.Anything, env).Return(nil)

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)

		// Simulate Python not being installed — version check
		// fails with an error.
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "--version")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", ""), fmt.Errorf("python not found")
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)

		err := runner.RunHooks(
			*mockContext.Context, HookTypePre, nil, "deploy",
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "preparing python hook")
	})

	t.Run("LanguageHookExecuteFailure", func(t *testing.T) {
		cwd := t.TempDir()
		ostest.Chdir(t, cwd)

		env := environment.NewWithValues("test", map[string]string{})

		scriptDir := filepath.Join(cwd, "hooks")
		require.NoError(t, os.MkdirAll(scriptDir, osutil.PermissionDirectory))
		scriptFile := filepath.Join(scriptDir, "predeploy.py")
		require.NoError(t, os.WriteFile(
			scriptFile, nil, osutil.PermissionExecutableFile,
		))

		hooksMap := map[string][]*HookConfig{
			"predeploy": {
				{
					Name: "predeploy",
					Run:  filepath.Join("hooks", "predeploy.py"),
				},
			},
		}

		envManager := &mockenv.MockEnvManager{}
		envManager.On("Reload", mock.Anything, env).Return(nil)

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)

		// Prepare succeeds (version check passes).
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "--version")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "Python 3.11.0", ""), nil
		})

		// Execute fails (script returns non-zero exit code).
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "predeploy.py")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(1, "", "error"), fmt.Errorf("script failed")
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)

		err := runner.RunHooks(
			*mockContext.Context, HookTypePre, nil, "deploy",
		)

		require.Error(t, err)
		require.Contains(t, err.Error(), "'predeploy' hook failed")
		require.Contains(t, err.Error(), "exit code: '1'")
	})

	t.Run("LanguageHookEnvVars", func(t *testing.T) {
		cwd := t.TempDir()
		ostest.Chdir(t, cwd)

		env := environment.NewWithValues("test", map[string]string{
			"MY_VAR":    "my_value",
			"OTHER_VAR": "other_value",
		})

		scriptDir := filepath.Join(cwd, "hooks")
		require.NoError(t, os.MkdirAll(scriptDir, osutil.PermissionDirectory))
		scriptFile := filepath.Join(scriptDir, "predeploy.py")
		require.NoError(t, os.WriteFile(
			scriptFile, nil, osutil.PermissionExecutableFile,
		))

		hooksMap := map[string][]*HookConfig{
			"predeploy": {
				{
					Name: "predeploy",
					Run:  filepath.Join("hooks", "predeploy.py"),
				},
			},
		}

		envManager := &mockenv.MockEnvManager{}
		envManager.On("Reload", mock.Anything, env).Return(nil)

		var capturedEnv []string

		mockContext := mocks.NewMockContext(context.Background())
		registerHookExecutors(mockContext)

		// Allow version check to pass.
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "--version")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			return exec.NewRunResult(0, "Python 3.11.0", ""), nil
		})

		// Capture environment variables passed to execution.
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "predeploy.py")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			capturedEnv = args.Env
			return exec.NewRunResult(0, "", ""), nil
		})

		hooksManager := NewHooksManager(cwd, mockContext.CommandRunner)
		runner := NewHooksRunner(
			hooksManager,
			mockContext.CommandRunner,
			envManager,
			mockContext.Console,
			cwd,
			hooksMap,
			env,
			mockContext.Container,
		)

		err := runner.RunHooks(
			*mockContext.Context, HookTypePre, nil, "deploy",
		)

		require.NoError(t, err)
		require.NotEmpty(t, capturedEnv)

		// The environment variables from the hook's env should
		// be forwarded to the executor.
		envMap := envSliceToMap(capturedEnv)
		require.Equal(t, "my_value", envMap["MY_VAR"])
		require.Equal(t, "other_value", envMap["OTHER_VAR"])
	})
}

// envSliceToMap converts a KEY=VALUE environment slice to a map.
func envSliceToMap(envVars []string) map[string]string {
	m := make(map[string]string, len(envVars))
	for _, entry := range envVars {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}
