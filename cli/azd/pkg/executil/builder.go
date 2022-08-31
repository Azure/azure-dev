package executil

import "context"

type Builder struct {
	commandRunnerFn RunCommandFn
	runArgs         RunArgs
}

// Creates a new builder with commandRunnerFn, cmd and args
func NewBuilder(cmd string, args ...string) *Builder {
	runArgs := NewRunArgs(cmd, args...)

	return &Builder{
		runArgs: runArgs,
	}
}

// Updates the commandRunnerFn that will used to execute commands
func (b *Builder) WithCommandRunner(commandRunnerFn RunCommandFn) *Builder {
	b.commandRunnerFn = commandRunnerFn
	return b
}

// Updates the cmd that will be executed
func (b *Builder) WithCmd(cmd string) *Builder {
	b.runArgs.Cmd = cmd
	return b
}

// Updates the command params that will get set
func (b *Builder) WithParams(params ...string) *Builder {
	b.runArgs.Args = params
	return b
}

// Updates the current working directory (cwd) for the command
func (b *Builder) WithCwd(cwd string) *Builder {
	b.runArgs.Cwd = cwd
	return b
}

// Updates the environment variables to used for the command
func (b *Builder) WithEnv(env []string) *Builder {
	b.runArgs.Env = env
	return b
}

// Updates whether or not this will be an interactive commands
// Interactive command sets stdin, stdout & stderr to the OS console/terminal
func (b *Builder) WithInteractive(interactive bool) *Builder {
	b.runArgs.Interactive = interactive
	return b
}

// Updates whether or not this will be run in a shell
func (b *Builder) WithShell(useShell bool) *Builder {
	b.runArgs.UseShell = useShell
	return b
}

// Updates whether or not errors will be enriched
func (b *Builder) WithEnrichError(enrichError bool) *Builder {
	b.runArgs.EnrichError = enrichError
	return b
}

// Builds the final command args
func (b *Builder) Build() RunArgs {
	return b.runArgs
}

// Executes the command with the configured command runner function and built arguments
func (b *Builder) Exec(ctx context.Context, commandRunnerFn RunCommandFn) (RunResult, error) {
	return commandRunnerFn(ctx, b.runArgs)
}
