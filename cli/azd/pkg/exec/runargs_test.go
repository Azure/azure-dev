package exec

import (
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
