// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ext

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_Hooks_Execute(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

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
				Shell: ShellTypeBash,
				Run:   "echo 'Hello'",
			},
		},
		"precommand": {
			{
				Shell: ShellTypeBash,
				Run:   "scripts/precommand.sh",
			},
		},
		"postcommand": {{
			Shell: ShellTypeBash,
			Run:   "scripts/postcommand.sh",
		},
		},
		"preinteractive": {
			{
				Shell:       ShellTypeBash,
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
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "precommand.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPreHook = true
			require.Equal(t, "scripts/precommand.sh", args.Args[0])
			require.Equal(t, cwd, args.Cwd)
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
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "postcommand.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPostHook = true
			require.Equal(t, "scripts/postcommand.sh", args.Args[0])
			require.Equal(t, cwd, args.Cwd)
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
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "preinteractive.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPostHook = true
			require.Equal(t, "scripts/preinteractive.sh", args.Args[0])
			require.Equal(t, cwd, args.Cwd)
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

	t.Run("Inline Hook Can Run Twice", func(t *testing.T) {
		var executedPaths []string

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return len(args.Args) == 1 && strings.Contains(args.Args[0], "azd-preinline-")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			executedPaths = append(executedPaths, args.Args[0])
			_, err := os.Stat(args.Args[0])
			require.NoError(t, err)

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
		require.NoError(t, err)
		require.Len(t, executedPaths, 1)

		_, err = os.Stat(executedPaths[0])
		require.Error(t, err)
		require.True(t, os.IsNotExist(err))

		err = runner.RunHooks(*mockContext.Context, HookTypePre, nil, "inline")
		require.NoError(t, err)
		require.Len(t, executedPaths, 2)
		require.NotEqual(t, executedPaths[0], executedPaths[1])

		_, err = os.Stat(executedPaths[1])
		require.Error(t, err)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("InvokeAction", func(t *testing.T) {
		ranPreHook := false
		ranPostHook := false
		ranAction := false

		hookLog := []string{}

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "precommand.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPreHook = true
			hookLog = append(hookLog, "pre")
			require.Equal(t, "scripts/precommand.sh", args.Args[0])

			return exec.NewRunResult(0, "", ""), nil
		})

		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "postcommand.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			ranPostHook = true
			hookLog = append(hookLog, "post")
			require.Equal(t, "scripts/postcommand.sh", args.Args[0])

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

func Test_Hooks_GetScript(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	env := environment.NewWithValues(
		"test",
		map[string]string{
			"a": "apple",
			"b": "banana",
		},
	)

	hooksMap := map[string][]*HookConfig{
		"bash": {
			{
				Run: "scripts/script.sh",
			},
		},
		"pwsh": {
			{
				Run: "scripts/script.ps1",
			},
		},
		"inline": {
			{
				Shell: ShellTypeBash,
				Run:   "echo 'hello'",
			},
		},
		"inlineWithUrl": {
			{
				Shell: ShellTypePowershell,
				Run:   "Invoke-WebRequest -Uri \"https://sample.com/sample.json\" -OutFile \"out.json\"",
			},
		},
	}

	ensureScriptsExist(t, hooksMap)

	envManager := &mockenv.MockEnvManager{}

	t.Run("Bash", func(t *testing.T) {
		hookConfig := hooksMap["bash"][0]
		mockContext := mocks.NewMockContext(context.Background())
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

		script, err := runner.GetScript(hookConfig, runner.env.Environ())
		require.NotNil(t, script)
		require.Equal(t, "*bash.bashScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationPath, hookConfig.location)
		require.Equal(t, ShellTypeBash, hookConfig.Shell)
		require.NoError(t, err)
	})

	t.Run("Powershell", func(t *testing.T) {
		hookConfig := hooksMap["pwsh"][0]
		mockContext := mocks.NewMockContext(context.Background())
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

		script, err := runner.GetScript(hookConfig, runner.env.Environ())
		require.NotNil(t, script)
		require.Equal(t, "*powershell.powershellScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationPath, hookConfig.location)
		require.Equal(t, ShellTypePowershell, hookConfig.Shell)
		require.NoError(t, err)
	})

	t.Run("Inline Script", func(t *testing.T) {
		tempDir := t.TempDir()
		ostest.Chdir(t, tempDir)

		hookConfig := hooksMap["inline"][0]
		mockContext := mocks.NewMockContext(context.Background())
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

		script, err := runner.GetScript(hookConfig, runner.env.Environ())
		require.NotNil(t, script)
		require.Equal(t, "*bash.bashScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationInline, hookConfig.location)
		require.Equal(t, ShellTypeBash, hookConfig.Shell)
		require.Equal(t, "echo 'hello'", hookConfig.script)
		require.Empty(t, hookConfig.path)
		require.NoError(t, err)
	})

	t.Run("Inline With Url", func(t *testing.T) {
		tempDir := t.TempDir()
		ostest.Chdir(t, tempDir)

		hookConfig := hooksMap["inlineWithUrl"][0]
		mockContext := mocks.NewMockContext(context.Background())
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

		script, err := runner.GetScript(hookConfig, runner.env.Environ())
		require.NotNil(t, script)
		require.Equal(t, "*powershell.powershellScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationInline, hookConfig.location)
		require.Equal(t, ShellTypePowershell, hookConfig.Shell)
		require.Contains(
			t,
			hookConfig.script,
			"Invoke-WebRequest -Uri \"https://sample.com/sample.json\" -OutFile \"out.json\"",
		)
		require.Empty(t, hookConfig.path)
		require.NoError(t, err)
	})

}

// Test_ExecHook_LanguageHooks verifies the integration between
// [HooksRunner] and [language.ScriptExecutor] for non-shell hooks.
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

	t.Run("ShellHookUnchanged", func(t *testing.T) {
		cwd := t.TempDir()
		ostest.Chdir(t, cwd)

		env := environment.NewWithValues("test", map[string]string{})

		hooksMap := map[string][]*HookConfig{
			"predeploy": {
				{
					Name:  "predeploy",
					Shell: ShellTypeBash,
					Run:   "scripts/predeploy.sh",
				},
			},
		}
		ensureScriptsExist(t, hooksMap)

		envManager := &mockenv.MockEnvManager{}
		envManager.On("Reload", mock.Anything, env).Return(nil)

		shellRan := false

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
			return strings.Contains(command, "predeploy.sh")
		}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
			shellRan = true
			require.Equal(t, "scripts/predeploy.sh", args.Args[0])
			require.Equal(t, cwd, args.Cwd)
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
		require.True(t, shellRan, "Shell script path should be used for .sh hooks")
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
		// be forwarded to the language executor.
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

type scriptValidationTest struct {
	name          string
	config        *HookConfig
	expectedError error
	createFile    bool
}

func Test_GetScript_Validation(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	err := os.WriteFile("my-script.ps1", nil, osutil.PermissionFile)
	require.NoError(t, err)

	env := environment.New("test")
	envManager := &mockenv.MockEnvManager{}

	mockContext := mocks.NewMockContext(context.Background())
	hooksManager := NewHooksManager(tempDir, mockContext.CommandRunner)
	runner := NewHooksRunner(
		hooksManager,
		mockContext.CommandRunner,
		envManager,
		mockContext.Console,
		tempDir,
		map[string][]*HookConfig{},
		env,
		mockContext.Container,
	)

	scriptValidations := []scriptValidationTest{
		{
			name: "Missing Script Type - Should Use Default Shell",
			config: &HookConfig{
				Name: "test1",
				Run:  "echo 'Hello'",
			},
			expectedError: nil, // Should no longer error, should use default shell
		},
		{
			name: "Missing Run param",
			config: &HookConfig{
				Name:  "test2",
				Shell: ShellTypeBash,
			},
			expectedError: ErrRunRequired,
		},
		{
			name: "Unsupported Script Type",
			config: &HookConfig{
				Name: "test4",
				Run:  "my-script.go",
			},
			expectedError: ErrUnsupportedScriptType,
			createFile:    true,
		},
		{
			name: "Valid External Script",
			config: &HookConfig{
				Name: "test5",
				Run:  "my-script.ps1",
			},
			createFile: true,
		},
		{
			name: "Valid Inline",
			config: &HookConfig{
				Name:  "test5",
				Shell: ShellTypeBash,
				Run:   "echo 'Hello'",
			},
		},
	}

	for _, test := range scriptValidations {
		if test.createFile {
			ensureScriptsExist(
				t,
				map[string][]*HookConfig{
					"test": {test.config},
				},
			)
		}

		t.Run(test.name, func(t *testing.T) {
			res, err := runner.GetScript(test.config, runner.env.Environ())
			if test.expectedError != nil {
				require.Nil(t, res)
				require.ErrorIs(t, err, test.expectedError)
			} else {
				require.NotNil(t, res)
				require.NoError(t, err)
			}
		})
	}
}
