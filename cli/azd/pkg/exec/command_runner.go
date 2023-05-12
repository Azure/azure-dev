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
	"regexp"
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

		if args.Stderr != nil {
			cmd.Stderr = io.MultiWriter(args.Stderr, &stderr)
		}
	}

	logTitle := strings.Builder{}
	logBody := strings.Builder{}
	defer func() {
		logTitle.WriteString(logBody.String())
		log.Print(logTitle.String())
	}()

	logTitle.WriteString(fmt.Sprintf("Run exec: '%s %s' ",
		args.Cmd,
		redactSensitiveData(
			strings.Join(redactSensitiveArgs(args.Args, args.SensitiveData), " "))))

	debugLogEnabled := r.debugLogging
	if args.DebugLogging != nil {
		debugLogEnabled = *args.DebugLogging
	}

	if debugLogEnabled && len(args.Env) > 0 {
		logBody.WriteString("Additional env:\n")
		for _, kv := range args.Env {
			logBody.WriteString(fmt.Sprintf("   %s\n", kv))
		}
	}

	if err := cmd.Start(); err != nil {
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
		if debugLogEnabled {
			logStdOut := strings.TrimSuffix(redactSensitiveData(stdout.String()), "\n")
			if len(logStdOut) > 0 {
				logBody.WriteString(fmt.Sprintf(
					"-------------------------------------stdout-------------------------------------------\n%s\n",
					logStdOut))
			}
			logStdErr := strings.TrimSuffix(redactSensitiveData(stderr.String()), "\n")
			if len(logStdErr) > 0 {
				logBody.WriteString(fmt.Sprintf(
					"-------------------------------------stderr-------------------------------------------\n%s\n",
					logStdErr))
			}

		}

		result = RunResult{
			ExitCode: cmd.ProcessState.ExitCode(),
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
		}
	}
	logTitle.WriteString(fmt.Sprintf(", exit code: %d\n", result.ExitCode))

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		outputAvailable := !args.Interactive
		err = NewExitError(*exitErr, result.Stdout, result.Stderr, outputAvailable)
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

	if err := process.Start(); err != nil {
		return NewRunResult(-1, "", ""), fmt.Errorf("error starting process: %w", err)
	}
	defer process.Kill()

	err = process.Wait()

	return NewRunResult(
		process.ProcessState.ExitCode(),
		stdOutBuf.String(),
		stdErrBuf.String(),
	), err
}

func appendEnv(env []string) []string {
	if len(env) > 0 {
		return append(os.Environ(), env...)
	}

	return nil
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

type redactData struct {
	matchString   *regexp.Regexp
	replaceString string
}

const cRedacted = "<redacted>"

func redactSensitiveArgs(args []string, sensitiveDataMatch []string) []string {
	if len(sensitiveDataMatch) == 0 {
		return args
	}
	redactedArgs := make([]string, len(args))
	for i, arg := range args {
		redacted := arg
		for _, sensitiveData := range sensitiveDataMatch {
			redacted = strings.ReplaceAll(redacted, sensitiveData, cRedacted)
		}
		redactedArgs[i] = redacted
	}
	return redactedArgs
}

func redactSensitiveData(msg string) string {
	var regexpRedactRules = map[string]redactData{
		"access token": {
			regexp.MustCompile("\"accessToken\": \".*\""),
			"\"accessToken\": \"" + cRedacted + "\"",
		},
		"deployment token": {
			regexp.MustCompile(`--deployment-token \S+`),
			"--deployment-token " + cRedacted,
		},
		"username": {
			regexp.MustCompile(`--username \S+`),
			"--username " + cRedacted,
		},
		"password": {
			regexp.MustCompile(`--password \S+`),
			"--password " + cRedacted,
		},
		"kubectl-from-literal": {
			regexp.MustCompile(`--from-literal=([^=]+)=(\S+)`),
			"--from-literal=$1=" + cRedacted,
		},
	}

	for _, redactRule := range regexpRedactRules {
		regMatchString := redactRule.matchString
		msg = regMatchString.ReplaceAllString(msg, redactRule.replaceString)
	}
	return msg
}
