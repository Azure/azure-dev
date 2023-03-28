package exec

import (
	"io"
)

// RunArgs exposes the command, arguments and other options when running console/shell commands
type RunArgs struct {
	Cmd  string
	Args []string
	Cwd  string
	Env  []string

	// Stderr will receive a copy of the text written to Stderr by
	// the command.
	// NOTE: RunResult.Stderr will still contain stderr output.
	Stderr io.Writer

	// Debug will `log.Printf` the command and it's results after it completes.
	Debug bool

	// EnrichError will include any command output if there is a failure
	// and output is available.
	// This is off by default.
	EnrichError bool

	// When set will run the command within a shell
	UseShell bool

	// When set will attach commands to std input/output
	Interactive bool

	// When set will call the command with the specified StdIn
	StdIn io.Reader
}

// NewRunArgs creates a new instance with the specified cmd and args
func NewRunArgs(cmd string, args ...string) RunArgs {
	return RunArgs{
		Cmd:  cmd,
		Args: args,
	}
}

// Appends additional command params
func (b RunArgs) AppendParams(params ...string) RunArgs {
	b.Args = append(b.Args, params...)
	return b
}

// Updates the current working directory (cwd) for the command
func (b RunArgs) WithCwd(cwd string) RunArgs {
	b.Cwd = cwd
	return b
}

// Updates the environment variables to used for the command
func (b RunArgs) WithEnv(env []string) RunArgs {
	b.Env = env
	return b
}

// Updates whether or not this will be an interactive commands
// Interactive command sets stdin, stdout & stderr to the OS console/terminal
func (b RunArgs) WithInteractive(interactive bool) RunArgs {
	b.Interactive = interactive
	return b
}

// Updates whether or not this will be run in a shell
func (b RunArgs) WithShell(useShell bool) RunArgs {
	b.UseShell = useShell
	return b
}

// Updates whether or not errors will be enriched
func (b RunArgs) WithEnrichError(enrichError bool) RunArgs {
	b.EnrichError = enrichError
	return b
}

// Updates whether or not debug output will be written to default logger
func (b RunArgs) WithDebug(debug bool) RunArgs {
	b.Debug = debug
	return b
}

// Updates the stdin reader that will be used while invoking the command
func (b RunArgs) WithStdIn(stdIn io.Reader) RunArgs {
	b.StdIn = stdIn
	return b
}
