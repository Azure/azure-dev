// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

// Contains support for automating the use of the azd CLI

package azdcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	HeartbeatInterval = 10 * time.Second
)

// sync.Once for one-time build for the process invocation
var buildOnce sync.Once

// The result of calling an azd CLI command
type CliResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// The azd CLI.
//
// Consumers should use the NewCLI constructor to initialize this struct.
type CLI struct {
	T                *testing.T
	WorkingDirectory string
	Env              []string
	// Path to the azd binary
	AzdPath string
}

// Constructs the CLI.
// On a local developer machine, this also ensures that the azd binary is up-to-date before running.
//
// By default, the path to the default source location is used, see GetSourcePath.
// Environment variable CLI_TEST_AZD_PATH can be used to set the path to the azd binary. This can be done in CI to
// run the tests against a specific azd binary.
//
// When CI is detected, no automatic build is performed. To disable automatic build behavior, CLI_TEST_SKIP_BUILD
// can be set to a truthy value.
func NewCLI(t *testing.T) *CLI {
	// Allow a override for custom build
	if os.Getenv("CLI_TEST_AZD_PATH") != "" {
		return &CLI{
			T:       t,
			AzdPath: os.Getenv("CLI_TEST_AZD_PATH"),
		}
	}

	// Reference the binary that is built in the same folder as source
	cliPath := GetSourcePath()
	if runtime.GOOS == "windows" {
		cliPath = filepath.Join(cliPath, "azd.exe")
	} else {
		cliPath = filepath.Join(cliPath, "azd")
	}

	cli := &CLI{
		T:       t,
		AzdPath: cliPath,
	}

	// Manual override for skipping automatic build
	skip, err := strconv.ParseBool(os.Getenv("CLI_TEST_SKIP_BUILD"))
	if err == nil && skip {
		return cli
	}

	// Skip automatic build in CI always
	if os.Getenv("CI") != "" ||
		strings.ToLower(os.Getenv("TF_BUILD")) == "true" ||
		strings.ToLower(os.Getenv("GITHUB_ACTIONS")) == "true" {
		return cli
	}

	buildOnce.Do(func() {
		cmd := exec.Command("go", "build")
		cmd.Dir = filepath.Dir(cliPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			panic(fmt.Errorf(
				"failed to build azd (ran %s in %s): %w:\n%s",
				strings.Join(cmd.Args, " "),
				cmd.Dir,
				err,
				output))
		}
	})

	return &CLI{
		T:       t,
		AzdPath: cliPath,
	}
}

func (cli *CLI) RunCommandWithStdIn(ctx context.Context, stdin string, args ...string) (*CliResult, error) {
	description := "azd " + strings.Join(args, " ") + " in " + cli.WorkingDirectory

	/* #nosec G204 - Subprocess launched with a potential tainted input or cmd arguments false positive */
	cmd := exec.CommandContext(ctx, cli.AzdPath, args...)
	if cli.WorkingDirectory != "" {
		cmd.Dir = cli.WorkingDirectory
	}

	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}

	cmd.Env = cli.Env

	// we run a background goroutine to report a heartbeat in the logs while the command
	// is still running. This makes it easy to see what's still in progress if we hit a timeout.
	done := make(chan struct{})
	go func() {
		cli.heartbeat(description, done)
	}()
	defer func() {
		done <- struct{}{}
	}()

	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	err := cmd.Run()

	result := &CliResult{}
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.ExitCode = cmd.ProcessState.ExitCode()

	for _, line := range strings.Split(result.Stdout, "\n") {
		cli.T.Logf("[stdout] %s", line)
	}

	for _, line := range strings.Split(result.Stderr, "\n") {
		cli.T.Logf("[stderr] %s", line)
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("command '%s' timed out: %w", description, err)
	}

	if errors.Is(ctx.Err(), context.Canceled) {
		// bubble up errors due to cancellation with their output, and let the caller
		// decide how to handle it.
		return result, ctx.Err()
	}

	if err != nil {
		return result, fmt.Errorf("command '%s' had non-zero exit code: %w", description, err)
	}

	return result, nil
}

func (cli *CLI) RunCommand(ctx context.Context, args ...string) (*CliResult, error) {
	return cli.RunCommandWithStdIn(ctx, "", args...)
}

func (cli *CLI) heartbeat(description string, done <-chan struct{}) {
	start := time.Now()
	for {
		select {
		case <-time.After(HeartbeatInterval):
			cli.T.Logf("[heartbeat] command %s is still running after %s", description, time.Since(start))
		case <-done:
			return
		}
	}
}

func GetSourcePath() string {
	_, b, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(b), "..", "..")
}
