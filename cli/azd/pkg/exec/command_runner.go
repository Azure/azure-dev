package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Settings to modify the way CmdTree is executed
type CmdTreeOptions struct {
	Interactive bool
}

// CommandRunner exposes the contract for executing console/shell commands for the specified runArgs
type CommandRunner interface {
	Run(ctx context.Context, args RunArgs) (RunResult, error)
	RunList(ctx context.Context, commands []string, args RunArgs) (RunResult, error)
}

type RunnerOptions struct {
	// Stdin is the input stream. If nil, os.Stdin is used.
	Stdin io.Reader
	// Stdout is the output stream. If nil, os.Stdout is used.
	Stdout io.Writer
	// Stderr is the error stream. If nil, os.Stderr is used.
	Stderr io.Writer
	// Whether debug logging is enabled. False by default.
	DebugLogging bool
}

// Creates a new default instance of the CommandRunner.
// Passing nil will use the default values for RunnerOptions.
//
// These options will be used by default during interactive commands
// unless specifically overridden within the command run arguments.
func NewCommandRunner(opt *RunnerOptions) CommandRunner {
	if opt == nil {
		opt = &RunnerOptions{}
	}

	runner := &commandRunner{
		stdin:        opt.Stdin,
		stdout:       opt.Stdout,
		stderr:       opt.Stderr,
		debugLogging: opt.DebugLogging,
	}

	if runner.stdin == nil {
		runner.stdin = os.Stdin
	}

	if runner.stdout == nil {
		runner.stdout = os.Stdout
	}

	if runner.stdout == nil {
		runner.stderr = os.Stderr
	}

	return runner
}

// commandRunner is the default private implementation of the CommandRunner interface
// This implementation executes actual commands on the underlying console/shell
type commandRunner struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// Whether debugLogging logging is enabled
	debugLogging bool
}

// Run runs the command specified in 'args'.
//
// Returns a RunResult that is the result of the command.
//   - If interactive is true, standard input/error is not captured in the returned result.
//     Instead, standard output/error is simply redirected to the os standard output/error.
//   - If the underlying command exits unsuccessfully, *ExitError is returned. Other possible errors would likely be I/O
//     errors or context cancellation.
//
// NOTE: on Windows the command will automatically be run within a shell. This means .bat/.cmd
// file based commands should just work.
func (r *commandRunner) Run(ctx context.Context, args RunArgs) (RunResult, error) {
	// use the shell on Windows since most commands are actually just batch files wrapping
	// real commands. And even if they're not, this will work fine without having to do any
	// probing or checking.
	cmd, err := newCmdTree(ctx, args.Cmd, args.Args, args.UseShell || runtime.GOOS == "windows", args.Interactive)

	if err != nil {
		return RunResult{}, err
	}

	cmd.Dir = args.Cwd

	var stdin io.Reader
	if args.StdIn != nil {
		stdin = args.StdIn
	} else {
		stdin = new(bytes.Buffer)
	}

	var stdout, stderr bytes.Buffer

	cmd.Env = appendEnv(args.Env)

	if args.Interactive {
		cmd.Stdin = r.stdin
		cmd.Stdout = r.stdout
		cmd.Stderr = r.stderr
	} else {
		cmd.Stdin = stdin
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if args.StdOut != nil {
			cmd.Stdout = io.MultiWriter(args.StdOut, &stdout)
		}

		if args.Stderr != nil {
			cmd.Stderr = io.MultiWriter(args.Stderr, &stderr)
		}
	}

	debugLogging := r.debugLogging
	if args.DebugLogging != nil {
		debugLogging = *args.DebugLogging
	}

	logMsg := logBuilder{
		args: append([]string{args.Cmd}, args.Args...),
		env:  args.Env,
	}
	defer func() {
		logMsg.Write(debugLogging, args.SensitiveData)
	}()

	if err := cmd.Start(); err != nil {
		logMsg.err = err
		return RunResult{}, err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		cmd.Kill()
	}()

	err = cmd.Wait()

	var result RunResult

	if args.Interactive {
		result = RunResult{
			ExitCode: cmd.ProcessState.ExitCode(),
			Stdout:   "",
			Stderr:   "",
		}
	} else {
		result = RunResult{
			ExitCode: cmd.ProcessState.ExitCode(),
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
		}
	}

	logMsg.result = &result

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		outputAvailable := !args.Interactive
		err = NewExitError(
			*exitErr,
			args.Cmd,
			result.Stdout,
			result.Stderr,
			outputAvailable)
	}

	return result, err
}

func (r *commandRunner) RunList(ctx context.Context, commands []string, args RunArgs) (RunResult, error) {
	process, err := newCmdTree(ctx, "", commands, true, false)
	if err != nil {
		return NewRunResult(-1, "", ""), err
	}

	process.Cmd.Dir = args.Cwd
	process.Env = appendEnv(args.Env)

	var stdOutBuf bytes.Buffer
	var stdErrBuf bytes.Buffer

	if process.Stdout == nil {
		process.Stdout = &stdOutBuf
	}

	if process.Stderr == nil {
		process.Stderr = &stdErrBuf
	}

	debugLogging := r.debugLogging
	if args.DebugLogging != nil {
		debugLogging = *args.DebugLogging
	}

	logMsg := logBuilder{
		// use the actual shell command invoked in the log message
		args: process.Cmd.Args,
		env:  args.Env,
	}
	defer func() {
		logMsg.Write(debugLogging, args.SensitiveData)
	}()

	if err := process.Start(); err != nil {
		logMsg.err = err
		return NewRunResult(-1, "", ""), fmt.Errorf("error starting process: %w", err)
	}
	defer process.Kill()

	err = process.Wait()
	result := NewRunResult(
		process.ProcessState.ExitCode(),
		stdOutBuf.String(),
		stdErrBuf.String(),
	)
	logMsg.result = &result

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		err = NewExitError(
			*exitErr,
			args.Cmd,
			result.Stdout,
			result.Stderr,
			true)
	}

	return result, err
}

func appendEnv(env []string) []string {
	if len(env) > 0 {
		return append(os.Environ(), env...)
	}

	return nil
}

// logBuilder builds messages for running of commands.
type logBuilder struct {
	args []string
	env  []string

	// Either result or err is expected to be set, but not both.
	result *RunResult
	err    error
}

// Write writes the log message to the log file. debug enables debug logging.
func (l *logBuilder) Write(debug bool, sensitiveArgsData []string) {
	msg := strings.Builder{}
	insensitiveArgs := RedactSensitiveArgs(l.args, sensitiveArgsData)
	msg.WriteString(fmt.Sprintf("Run exec: '%s' ", RedactSensitiveData(strings.Join(insensitiveArgs, " "))))
	if l.result != nil {
		msg.WriteString(fmt.Sprintf(", exit code: %d\n", l.result.ExitCode))
	} else if l.err != nil {
		msg.WriteString(fmt.Sprintf(", err: %v\n", l.err))
	}

	if debug && len(l.env) > 0 {
		msg.WriteString("Additional env:\n")
		for _, kv := range l.env {
			msg.WriteString(fmt.Sprintf("   %s\n", RedactSensitiveData(kv)))
		}
	}

	if debug && l.result != nil && len(l.result.Stdout) > 0 {
		logStdOut := strings.TrimSuffix(RedactSensitiveData(l.result.Stdout), "\n")
		if len(logStdOut) > 0 {
			msg.WriteString(fmt.Sprintf(
				"-------------------------------------stdout-------------------------------------------\n%s\n",
				logStdOut))
		}
	}

	if debug && l.result != nil && len(l.result.Stderr) > 0 {
		logStdErr := strings.TrimSuffix(RedactSensitiveData(l.result.Stderr), "\n")
		if len(logStdErr) > 0 {
			msg.WriteString(fmt.Sprintf(
				"-------------------------------------stderr-------------------------------------------\n%s\n",
				logStdErr))
		}
	}

	log.Print(msg.String())
}

// newCmdTree creates a `CmdTree`, optionally using a shell appropriate for windows
// or POSIX environments.
// An empty cmd parameter indicates "command list mode", which means that args are combined into a single command list,
// joined with && operator.
func newCmdTree(ctx context.Context, cmd string, args []string, useShell bool, interactive bool) (CmdTree, error) {
	options := CmdTreeOptions{Interactive: interactive}

	if !useShell {
		if cmd == "" {
			return CmdTree{}, errors.New("command must be provided if shell is not used")
		} else {
			return CmdTree{
				CmdTreeOptions: options,
				Cmd:            exec.CommandContext(ctx, cmd, args...),
			}, nil
		}
	}

	var shellName string
	var shellCommandPrefix string

	if runtime.GOOS == "windows" {
		dir := os.Getenv("SYSTEMROOT")
		if dir == "" {
			return CmdTree{}, errors.New("environment variable 'SYSTEMROOT' has no value")
		}

		shellName = filepath.Join(dir, "System32", "cmd.exe")
		shellCommandPrefix = "/c"

		if cmd == "" {
			args = []string{strings.Join(args, " && ")}
		} else {
			args = append([]string{cmd}, args...)
		}
	} else {
		shellName = filepath.Join("/", "bin", "sh")
		shellCommandPrefix = "-c"

		if cmd == "" {
			args = []string{strings.Join(args, " && ")}
		} else {
			var cmdBuilder strings.Builder
			cmdBuilder.WriteString(cmd)

			for i := range args {
				cmdBuilder.WriteString(" \"$")
				fmt.Fprintf(&cmdBuilder, "%d", i)
				cmdBuilder.WriteString("\"")
			}

			args = append([]string{cmdBuilder.String()}, args...)
		}
	}

	var allArgs []string
	allArgs = append(allArgs, shellCommandPrefix)
	allArgs = append(allArgs, args...)

	return CmdTree{
		CmdTreeOptions: options,
		Cmd:            exec.Command(shellName, allArgs...),
	}, nil
}
