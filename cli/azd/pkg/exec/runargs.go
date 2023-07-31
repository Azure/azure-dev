package exec

import (
	"io"
)

// RunArgs exposes the command, arguments and other options when running console/shell commands
type RunArgs struct {
	Cmd  string
	Args []string
	// Any string from SensitiveData will be redacted as *** if found in Args
	SensitiveData []string
	Cwd           string
	Env           []string

	// Stderr will receive a copy of the text written to Stderr by
	// the command.
	// NOTE: RunResult.Stderr will still contain stderr output.
	Stderr io.Writer

	// Enables debug logging.
	DebugLogging *bool

	// When set will run the command within a shell
	UseShell bool

	// When set will attach commands to std input/output
	Interactive bool

	// When set will call the command with the specified StdIn
	StdIn io.Reader

	// When set will call the command with the specified StdOut
	StdOut io.Writer
}

// NewRunArgs creates a new instance with the specified cmd and args
func NewRunArgs(cmd string, args ...string) RunArgs {
	return RunArgs{
		Cmd:  cmd,
		Args: args,
	}
}

// NewRunArgs creates a new instance with the specified cmd and args and a list of SensitiveData
// Use this constructor to protect known sensitive data from going to logs
func NewRunArgsWithSensitiveData(cmd string, args, sensitiveData []string) RunArgs {
	return RunArgs{
		Cmd:           cmd,
		Args:          args,
		SensitiveData: sensitiveData,
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

// Updates whether or not debug output will be written to default logger
func (b RunArgs) WithDebugLogging(debug bool) RunArgs {
	b.DebugLogging = &debug
	return b
}

// Updates the stdin reader that will be used while invoking the command
func (b RunArgs) WithStdIn(stdIn io.Reader) RunArgs {
	b.StdIn = stdIn
	return b
}

// Updates the stdout writer that will be used while invoking the command
func (b RunArgs) WithStdOut(stdOut io.Writer) RunArgs {
	b.StdOut = stdOut
	return b
}

// Updates the stderr writer that will be used while invoking the command
func (b RunArgs) WithStdErr(stdErr io.Writer) RunArgs {
	b.Stderr = stdErr
	return b
}
