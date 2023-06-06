package exec

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRunArgs(t *testing.T) {
	t.Run("WithDefaults", func(t *testing.T) {
		runArgs := NewRunArgs("az", "login")

		require.Equal(t, "az", runArgs.Cmd)
		require.Len(t, runArgs.Args, 1)
		require.Equal(t, []string{"login"}, runArgs.Args)
		require.Equal(t, false, runArgs.Interactive)
		require.Equal(t, false, runArgs.UseShell)
		require.Equal(t, "", runArgs.Cwd)
		require.Nil(t, runArgs.DebugLogging)
		require.Len(t, runArgs.Env, 0)
	})

	t.Run("WithOverrides", func(t *testing.T) {
		runArgs := NewRunArgs("az", "login").
			WithCwd("cwd").
			WithEnv([]string{"foo", "bar"}).
			WithInteractive(true).
			WithShell(true).
			WithDebugLogging(true).
			AppendParams("param1", "param2")

		require.Equal(t, "az", runArgs.Cmd)
		require.Len(t, runArgs.Args, 3)
		require.Equal(t, []string{"login", "param1", "param2"}, runArgs.Args)
		require.Equal(t, true, runArgs.Interactive)
		require.Equal(t, true, runArgs.UseShell)
		require.Equal(t, "cwd", runArgs.Cwd)
		require.Equal(t, true, *runArgs.DebugLogging)
		require.Len(t, runArgs.Env, 2)
		require.Equal(t, runArgs.Env, []string{"foo", "bar"})
	})
}

type list struct {
	t string
}

func TestInteLimited(t *testing.T) {
	t.Run("limited", func(t *testing.T) {

		args := NewRunArgs("sudo", "docker", "build", "/home/vivazqu/workspace/build/openai/.devcontainer", "--progress=plain")
		args = args.WithInteractive(true)

		ctx := context.Background()
		cmd, err := NewCmdTree(ctx, args.Cmd, args.Args, args.UseShell || runtime.GOOS == "windows", args.Interactive)
		require.NoError(t, err)

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Start()

		// scanner := bufio.NewScanner(stdout)
		// scanner.Split(bufio.ScanRunes)
		// for scanner.Scan() {
		// 	m := scanner.Text()
		// 	fmt.Println(m)
		// }
		cmd.Wait()
	})

}
