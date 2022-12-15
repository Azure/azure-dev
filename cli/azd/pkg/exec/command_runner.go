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

// CommandRunner exposes the contract for executing console/shell commands for the specified runArgs
type CommandRunner interface {
	Run(ctx context.Context, args RunArgs) (RunResult, error)
}

// Creates a new default instance of the CommandRunner
func NewCommandRunner(stdin io.Reader, stdout io.Writer, stderr io.Writer) CommandRunner {
	return &commandRunner{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
}

// commandRunner is the default private implementation of the CommandRunner interface
// This implementation executes actual commands on the underlying console/shell
type commandRunner struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// Run runs the command specified in 'args'.
//
// If the underlying command exits with a non-zero exit code you will get an error _and_ a RunResult.
// If you would like to automatically include the stdout/stderr of the process in the returned error you can
// set RunArgs.EnrichError to 'true', which means your code can just check and return 'error' without having
// to inspect the RunResult.
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

	log.Printf("Run exec: '%s %s'", args.Cmd, redactSensitiveData(strings.Join(args.Args, " ")))

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
			log.Printf(
				"Exit Code:%d\nOut:%s\nErr:%s\n",
				cmd.ProcessState.ExitCode(),
				redactSensitiveData(stdout.String()),
				redactSensitiveData(stderr.String()))
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

func redactSensitiveData(msg string) string {
	var regexpRedactRules = map[string]redactData{
		"access token": {
			regexp.MustCompile("\"accessToken\": \".*\""),
			"\"accessToken\": \"<redacted>\"",
		},
		"deployment token": {
			regexp.MustCompile(`--deployment-token \S+`),
			"--deployment-token <redacted>",
		},
		"username": {
			regexp.MustCompile(`--username \S+`),
			"--username <redacted>",
		},
		"password": {
			regexp.MustCompile(`--password \S+`),
			"--password <redacted>",
		}}

	for _, redactRule := range regexpRedactRules {
		regMatchString := redactRule.matchString
		msg = regMatchString.ReplaceAllString(msg, redactRule.replaceString)
	}
	return msg
}
