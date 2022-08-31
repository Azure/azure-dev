// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package executil

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

type RunCommandFn func(ctx context.Context, args RunArgs) (RunResult, error)
type contextKey string

const (
	execFnContextKey contextKey = "execfn"
)

// Gets the exec util implementation used for executing CLI commands on the host machine
// This override should ONLY be called during unit testing, otherwise the default implementation is used.
func WithCommandRunner(ctx context.Context, execFn RunCommandFn) context.Context {
	return context.WithValue(ctx, execFnContextKey, execFn)
}

// Gets the exec util implementation used for executing cLI commands on the host machine
// If a value is not found in the context the default implementation will be used.
func GetCommandRunner(ctx context.Context) RunCommandFn {
	execFn, ok := ctx.Value(execFnContextKey).(RunCommandFn)
	if !ok {
		return RunWithResult
	}

	return execFn
}

// Settings to modify the way CmdTree is executed
type CmdTreeOptions struct {
	Interactive bool
}

// RunCommandList runs a list of commands in shell.
// The command list is constructed using '&&' operator, so the first failing command causes the whole list run to fail.
func RunCommandList(ctx context.Context, commands []string, env []string, cwd string) (RunResult, error) {
	process, err := newCmdTree(ctx, "", commands, true)
	if err != nil {
		return NewRunResult(-1, "", ""), err
	}

	process.Cmd.Dir = cwd
	process.Env = appendEnv(env)

	return execCmdTree(process)
}

func execCmdTree(process CmdTree) (RunResult, error) {
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

	err := process.Wait()

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
func newCmdTree(ctx context.Context, cmd string, args []string, useShell bool) (CmdTree, error) {
	if !useShell {
		if cmd == "" {
			return CmdTree{}, errors.New("command must be provided if shell is not used")
		} else {
			return CmdTree{Cmd: exec.CommandContext(ctx, cmd, args...)}, nil
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

	return CmdTree{Cmd: exec.Command(shellName, allArgs...)}, nil
}

type RunResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func NewRunResult(code int, stdout, stderr string) RunResult {
	return RunResult{
		ExitCode: code,
		Stdout:   stdout,
		Stderr:   stderr,
	}
}

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
}

func NewRunArgs(cmd string, args ...string) RunArgs {
	return RunArgs{
		Cmd:  cmd,
		Args: args,
	}
}

type redactData struct {
	matchString   *regexp.Regexp
	replaceString string
}

func (rr RunResult) String() string {
	return fmt.Sprintf("exit code: %d, stdout: %s, stderr: %s", rr.ExitCode, rr.Stdout, rr.Stderr)
}

func redactSensitiveData(msg string) string {
	var regexpRedactRules = map[string]redactData{
		"access token": {
			regexp.MustCompile("\"accessToken\": \".*\""),
			"\"accessToken\": \"<redacted>\"",
		}}

	for _, redactRule := range regexpRedactRules {
		regMatchString := redactRule.matchString
		return regMatchString.ReplaceAllString(msg, redactRule.replaceString)
	}
	return msg
}

// RunWithResult runs the command specified in 'args'.
//
// If the underlying command exits with a non-zero exit code you will get an error _and_ a RunResult.
// If you would like to automatically include the stdout/stderr of the process in the returned error you can
// set RunArgs.EnrichError to 'true', which means your code can just check and return 'error' without having
// to inspect the RunResult.
//
// NOTE: on Windows the command will automatically be run within a shell. This means .bat/.cmd
// file based commands should just work.
func RunWithResult(ctx context.Context, args RunArgs) (RunResult, error) {
	// use the shell on Windows since most commands are actually just batch files wrapping
	// real commands. And even if they're not, this will work fine without having to do any
	// probing or checking.
	cmd, err := newCmdTree(ctx, args.Cmd, args.Args, args.UseShell || runtime.GOOS == "windows")

	if err != nil {
		return RunResult{}, err
	}

	cmd.Dir = args.Cwd

	var stdin, stdout, stderr bytes.Buffer

	cmd.Env = appendEnv(args.Env)

	if args.Interactive {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdin = &stdin
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if args.Stderr != nil {
			cmd.Stderr = io.MultiWriter(args.Stderr, &stderr)
		}
	}

	log.Printf("RunWithResult exec: '%s %s'", args.Cmd, strings.Join(args.Args, " "))

	if args.Debug && len(args.Env) > 0 {
		log.Println("Additional env:")
		for _, kv := range args.Env {
			log.Printf("  %s", kv)
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
		if args.Debug {
			log.Printf("Exit Code:%d\nOut:%s\nErr:%s\n", cmd.ProcessState.ExitCode(), redactSensitiveData(stdout.String()), redactSensitiveData(stderr.String()))
		}

		result = RunResult{
			ExitCode: cmd.ProcessState.ExitCode(),
			Stdout:   stdout.String(),
			Stderr:   stderr.String(),
		}
	}

	if err != nil && args.EnrichError {
		err = fmt.Errorf("%s: %w", result, err)
	}

	return result, err
}
