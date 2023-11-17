package ext

import (
	"context"
	"os"
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

	hooks := map[string]*HookConfig{
		"preinline": {
			Shell: ShellTypeBash,
			Run:   "echo 'Hello'",
		},
		"precommand": {
			Shell: ShellTypeBash,
			Run:   "scripts/precommand.sh",
		},
		"postcommand": {
			Shell: ShellTypeBash,
			Run:   "scripts/postcommand.sh",
		},
		"preinteractive": {
			Shell:       ShellTypeBash,
			Run:         "scripts/preinteractive.sh",
			Interactive: true,
		},
	}

	ensureScriptsExist(t, hooks)

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

		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)
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

		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)
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

		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)
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

		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)
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

		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)
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

	hooks := map[string]*HookConfig{
		"bash": {
			Run: "scripts/script.sh",
		},
		"pwsh": {
			Run: "scripts/script.ps1",
		},
		"inline": {
			Shell: ShellTypeBash,
			Run:   "echo 'hello'",
		},
		"inlineWithUrl": {
			Shell: ShellTypePowershell,
			Run:   "Invoke-WebRequest -Uri \"https://sample.com/sample.json\" -OutFile \"out.json\"",
		},
	}

	ensureScriptsExist(t, hooks)

	envManager := &mockenv.MockEnvManager{}

	t.Run("Bash", func(t *testing.T) {
		hookConfig := hooks["bash"]
		mockContext := mocks.NewMockContext(context.Background())
		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)

		script, err := runner.GetScript(hookConfig)
		require.NotNil(t, script)
		require.Equal(t, "*bash.bashScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationPath, hookConfig.location)
		require.Equal(t, ShellTypeBash, hookConfig.Shell)
		require.NoError(t, err)
	})

	t.Run("Powershell", func(t *testing.T) {
		hookConfig := hooks["pwsh"]
		mockContext := mocks.NewMockContext(context.Background())
		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)

		script, err := runner.GetScript(hookConfig)
		require.NotNil(t, script)
		require.Equal(t, "*powershell.powershellScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationPath, hookConfig.location)
		require.Equal(t, ShellTypePowershell, hookConfig.Shell)
		require.NoError(t, err)
	})

	t.Run("Inline Script", func(t *testing.T) {
		tempDir := t.TempDir()
		ostest.Chdir(t, tempDir)

		hookConfig := hooks["inline"]
		mockContext := mocks.NewMockContext(context.Background())
		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)

		script, err := runner.GetScript(hookConfig)
		require.NotNil(t, script)
		require.Equal(t, "*bash.bashScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationInline, hookConfig.location)
		require.Equal(t, ShellTypeBash, hookConfig.Shell)
		require.Contains(t, hookConfig.path, os.TempDir())
		require.Contains(t, hookConfig.path, ".sh")
		require.NoError(t, err)

		fileInfo, err := os.Stat(hookConfig.path)
		require.NotNil(t, fileInfo)
		require.NoError(t, err)
	})

	t.Run("Inline With Url", func(t *testing.T) {
		tempDir := t.TempDir()
		ostest.Chdir(t, tempDir)

		hookConfig := hooks["inlineWithUrl"]
		mockContext := mocks.NewMockContext(context.Background())
		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, envManager, mockContext.Console, cwd, hooks, env)

		script, err := runner.GetScript(hookConfig)
		require.NotNil(t, script)
		require.Equal(t, "*powershell.powershellScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationInline, hookConfig.location)
		require.Equal(t, ShellTypePowershell, hookConfig.Shell)
		require.Contains(
			t,
			hookConfig.script,
			"Invoke-WebRequest -Uri \"https://sample.com/sample.json\" -OutFile \"out.json\"",
		)
		require.Contains(t, hookConfig.path, os.TempDir())
		require.Contains(t, hookConfig.path, ".ps1")
		require.NoError(t, err)

		fileInfo, err := os.Stat(hookConfig.path)
		require.NotNil(t, fileInfo)
		require.NoError(t, err)
	})

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
	hooksManager := NewHooksManager(tempDir)
	runner := NewHooksRunner(
		hooksManager,
		mockContext.CommandRunner,
		envManager,
		mockContext.Console,
		tempDir,
		map[string]*HookConfig{},
		env,
	)

	scriptValidations := []scriptValidationTest{
		{
			name: "Missing Script Type",
			config: &HookConfig{
				Name: "test1",
				Run:  "echo 'Hello'",
			},
			expectedError: ErrScriptTypeUnknown,
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
			ensureScriptsExist(t, map[string]*HookConfig{"test": test.config})
		}

		t.Run(test.name, func(t *testing.T) {
			res, err := runner.GetScript(test.config)
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
