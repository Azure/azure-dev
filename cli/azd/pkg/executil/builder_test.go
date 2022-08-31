package executil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewBuilder(t *testing.T) {
	t.Run("WithDefaults", func(t *testing.T) {
		runArgs := NewBuilder("az", "login").Build()

		require.Equal(t, "az", runArgs.Cmd)
		require.Len(t, runArgs.Args, 1)
		require.Equal(t, []string{"login"}, runArgs.Args)
		require.Equal(t, false, runArgs.Interactive)
		require.Equal(t, false, runArgs.UseShell)
		require.Equal(t, false, runArgs.EnrichError)
		require.Equal(t, "", runArgs.Cwd)
		require.Equal(t, false, runArgs.EnrichError)
		require.Len(t, runArgs.Env, 0)
	})

	t.Run("WithOverrides", func(t *testing.T) {
		runArgs := NewBuilder("az", "login").
			WithCmd("anothercmd").
			WithCwd("cwd").
			WithEnv([]string{"foo", "bar"}).
			WithInteractive(true).
			WithShell(true).
			WithEnrichError(true).
			WithParams("param1", "param2").
			Build()

		require.Equal(t, "anothercmd", runArgs.Cmd)
		require.Len(t, runArgs.Args, 2)
		require.Equal(t, []string{"param1", "param2"}, runArgs.Args)
		require.Equal(t, true, runArgs.Interactive)
		require.Equal(t, true, runArgs.UseShell)
		require.Equal(t, true, runArgs.EnrichError)
		require.Equal(t, "cwd", runArgs.Cwd)
		require.Equal(t, true, runArgs.EnrichError)
		require.Len(t, runArgs.Env, 2)
		require.Equal(t, runArgs.Env, []string{"foo", "bar"})
	})
}

func TestExec(t *testing.T) {
	ranCommand := false
	builder := NewBuilder("az", "login")
	expectedArgs := builder.Build()

	commandRunner := func(ctx context.Context, runArgs RunArgs) (RunResult, error) {
		ranCommand = true
		require.Equal(t, expectedArgs, runArgs)
		return NewRunResult(0, "", ""), nil
	}

	res, err := builder.Exec(context.Background(), commandRunner)

	require.NotNil(t, res)
	require.NoError(t, err)
	require.True(t, ranCommand)
}
