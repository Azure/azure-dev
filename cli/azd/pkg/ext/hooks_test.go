package ext

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/stretchr/testify/require"
)

func Test_Hooks_Execute(t *testing.T) {
	cwd := t.TempDir()
	ostest.Chdir(t, cwd)

	env := []string{
		"a=apple",
		"b=banana",
	}

	err := os.Mkdir("scripts", osutil.PermissionDirectory)
	require.NoError(t, err)
	err = os.WriteFile("scripts/precommand.sh", []byte("echo 'precommand'"), osutil.PermissionExecutableFile)
	require.NoError(t, err)
	err = os.WriteFile("scripts/postcommand.sh", []byte("echo 'postcommand'"), osutil.PermissionExecutableFile)
	require.NoError(t, err)
	err = os.WriteFile("scripts/preinteractive.sh", []byte("echo 'preinteractive'"), osutil.PermissionExecutableFile)
	require.NoError(t, err)

	scripts := map[string]*ScriptConfig{
		"precommand": {
			Type:     ScriptTypeBash,
			Location: ScriptLocationPath,
			Path:     "scripts/precommand.sh",
		},
		"postcommand": {
			Type:     ScriptTypeBash,
			Location: ScriptLocationPath,
			Path:     "scripts/postcommand.sh",
		},
		"preinteractive": {
			Type:        ScriptTypeBash,
			Location:    ScriptLocationPath,
			Path:        "scripts/preinteractive.sh",
			Interactive: true,
		},
	}

	t.Run("Execute", func(t *testing.T) {
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
				require.Equal(t, env, args.Env)
				require.Equal(t, false, args.Interactive)

				return exec.NewRunResult(0, "", ""), nil
			})

			hooksManager := NewHooksManager(cwd)
			runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, mockContext.Console, cwd, scripts, env)
			err := runner.RunHooks(*mockContext.Context, HookTypePre, []string{"command"})

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
				require.Equal(t, env, args.Env)
				require.Equal(t, false, args.Interactive)

				return exec.NewRunResult(0, "", ""), nil
			})

			hooksManager := NewHooksManager(cwd)
			runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, mockContext.Console, cwd, scripts, env)
			err := runner.RunHooks(*mockContext.Context, HookTypePre, []string{"command"})

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
				require.Equal(t, env, args.Env)
				require.Equal(t, true, args.Interactive)

				return exec.NewRunResult(0, "", ""), nil
			})

			hooksManager := NewHooksManager(cwd)
			runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, mockContext.Console, cwd, scripts, env)
			err := runner.RunHooks(*mockContext.Context, HookTypePre, []string{"command"})

			require.False(t, ranPreHook)
			require.True(t, ranPostHook)
			require.NoError(t, err)
		})
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
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, mockContext.Console, cwd, scripts, env)
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

	env := []string{
		"a=apple",
		"b=banana",
	}

	err := os.WriteFile("script.sh", []byte("echo 'Hello'"), osutil.PermissionFile)
	require.NoError(t, err)
	err = os.WriteFile("script.ps1", []byte("Write-Host \"Hello\""), osutil.PermissionFile)
	require.NoError(t, err)

	scripts := map[string]*ScriptConfig{
		"bash": {
			Path: "script.sh",
		},
		"pwsh": {
			Path: "script.ps1",
		},
		"inline": {
			Type:   ScriptTypeBash,
			Script: "echo 'hello'",
		},
	}

	t.Run("Bash", func(t *testing.T) {
		scriptConfig := scripts["bash"]
		mockContext := mocks.NewMockContext(context.Background())
		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, mockContext.Console, cwd, scripts, env)

		script, err := runner.GetScript(scriptConfig)
		require.NotNil(t, script)
		require.Equal(t, "*bash.bashScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationPath, scriptConfig.Location)
		require.Equal(t, ScriptTypeBash, scriptConfig.Type)
		require.NoError(t, err)
	})

	t.Run("Powershell", func(t *testing.T) {
		scriptConfig := scripts["pwsh"]
		mockContext := mocks.NewMockContext(context.Background())
		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, mockContext.Console, cwd, scripts, env)

		script, err := runner.GetScript(scriptConfig)
		require.NotNil(t, script)
		require.Equal(t, "*powershell.powershellScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationPath, scriptConfig.Location)
		require.Equal(t, ScriptTypePowershell, scriptConfig.Type)
		require.NoError(t, err)
	})

	t.Run("Inline Script", func(t *testing.T) {
		tempDir := t.TempDir()
		ostest.Chdir(t, tempDir)

		scriptConfig := scripts["inline"]
		mockContext := mocks.NewMockContext(context.Background())
		hooksManager := NewHooksManager(cwd)
		runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, mockContext.Console, cwd, scripts, env)

		script, err := runner.GetScript(scriptConfig)
		require.NotNil(t, script)
		require.Equal(t, "*bash.bashScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationInline, scriptConfig.Location)
		require.Equal(t, ScriptTypeBash, scriptConfig.Type)
		require.Contains(t, scriptConfig.Path, fmt.Sprintf(".azure%chooks", os.PathSeparator))
		require.Contains(t, scriptConfig.Path, ".sh")
		require.NoError(t, err)

		fileInfo, err := os.Stat(scriptConfig.Path)
		require.NotNil(t, fileInfo)
		require.NoError(t, err)
	})
}

type scriptValidationTest struct {
	name          string
	config        *ScriptConfig
	expectedError error
}

func Test_GetScript_Validation(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	err := os.WriteFile("my-script.ps1", nil, osutil.PermissionFile)
	require.NoError(t, err)

	env := []string{}

	mockContext := mocks.NewMockContext(context.Background())
	hooksManager := NewHooksManager(tempDir)
	runner := NewHooksRunner(hooksManager, mockContext.CommandRunner, mockContext.Console, tempDir, map[string]*ScriptConfig{}, env)

	scriptValidations := []scriptValidationTest{
		{
			name: "Missing Script Type",
			config: &ScriptConfig{
				Name:   "test1",
				Script: "echo 'Hello'",
			},
			expectedError: ErrScriptTypeUnknown,
		},
		{
			name: "Missing Script for Location Inline",
			config: &ScriptConfig{
				Name:     "test2",
				Type:     ScriptTypeBash,
				Location: ScriptLocationInline,
			},
			expectedError: ErrScriptRequired,
		},
		{
			name: "Missing Path for Location Path",
			config: &ScriptConfig{
				Name:     "test3",
				Type:     ScriptTypeBash,
				Location: ScriptLocationPath,
			},
			expectedError: ErrPathRequired,
		},
		{
			name: "Unsupported Script Type",
			config: &ScriptConfig{
				Name: "test4",
				Path: "my-script.go",
			},
			expectedError: ErrUnsupportedScriptType,
		},
		{
			name: "Invalid External Script",
			config: &ScriptConfig{
				Name: "test5",
				Path: "no-exist.ps1",
			},
			expectedError: os.ErrNotExist,
		},
		{
			name: "Valid External Script",
			config: &ScriptConfig{
				Name: "test5",
				Path: "my-script.ps1",
			},
		},
		{
			name: "Valid Inline",
			config: &ScriptConfig{
				Name:   "test5",
				Type:   ScriptTypeBash,
				Script: "echo 'Hello'",
			},
		},
	}

	for _, test := range scriptValidations {
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
