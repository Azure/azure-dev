package ext

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_Hooks_Execute(t *testing.T) {
	cwd := t.TempDir()
	env := []string{
		"a=apple",
		"b=banana",
	}

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

			hooks := NewCommandHooks(mockContext.CommandRunner, mockContext.Console, scripts, cwd, env)
			err := hooks.RunScripts(*mockContext.Context, HookTypePre, []string{"command"})

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

			hooks := NewCommandHooks(mockContext.CommandRunner, mockContext.Console, scripts, cwd, env)
			err := hooks.RunScripts(*mockContext.Context, HookTypePost, []string{"command"})

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

			hooks := NewCommandHooks(mockContext.CommandRunner, mockContext.Console, scripts, cwd, env)
			err := hooks.RunScripts(*mockContext.Context, HookTypePre, []string{"interactive"})

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

		hooks := NewCommandHooks(mockContext.CommandRunner, mockContext.Console, scripts, cwd, env)
		err := hooks.Invoke(*mockContext.Context, []string{"command"}, func() error {
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
	env := []string{
		"a=apple",
		"b=banana",
	}

	scripts := map[string]*ScriptConfig{
		"bash": {
			Path: "scripts/script.sh",
		},
		"pwsh": {
			Path: "scripts/script.ps1",
		},
		"inline": {
			Type:   ScriptTypeBash,
			Script: "echo 'hello'",
		},
	}

	t.Run("Bash", func(t *testing.T) {
		scriptConfig := scripts["bash"]
		mockContext := mocks.NewMockContext(context.Background())
		hooks := NewCommandHooks(mockContext.CommandRunner, mockContext.Console, scripts, cwd, env)

		script, err := hooks.GetScript(scriptConfig)
		require.NotNil(t, script)
		require.Equal(t, "*bash.bashScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationPath, scriptConfig.Location)
		require.Equal(t, ScriptTypeBash, scriptConfig.Type)
		require.NoError(t, err)
	})

	t.Run("Powershell", func(t *testing.T) {
		scriptConfig := scripts["pwsh"]
		mockContext := mocks.NewMockContext(context.Background())
		hooks := NewCommandHooks(mockContext.CommandRunner, mockContext.Console, scripts, cwd, env)

		script, err := hooks.GetScript(scriptConfig)
		require.NotNil(t, script)
		require.Equal(t, "*powershell.powershellScript", reflect.TypeOf(script).String())
		require.Equal(t, ScriptLocationPath, scriptConfig.Location)
		require.Equal(t, ScriptTypePowershell, scriptConfig.Type)
		require.NoError(t, err)
	})

	t.Run("Inline Script", func(t *testing.T) {
		wd, err := os.Getwd()
		require.NoError(t, err)

		tempDir := t.TempDir()
		err = os.Chdir(tempDir)
		require.NoError(t, err)

		t.Cleanup(func() {
			err := os.Chdir(wd)
			require.NoError(t, err)
		})

		scriptConfig := scripts["inline"]
		mockContext := mocks.NewMockContext(context.Background())
		hooks := NewCommandHooks(mockContext.CommandRunner, mockContext.Console, scripts, tempDir, env)

		script, err := hooks.GetScript(scriptConfig)
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
