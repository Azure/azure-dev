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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	HeartbeatInterval = 10 * time.Second
)

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
	// The location of azd to invoke. By default, set to GetAzdLocation()
	AzdLocation string
}

func NewCLI(t *testing.T) *CLI {
	return &CLI{
		T:           t,
		AzdLocation: GetAzdLocation(),
	}
}

func (cli *CLI) RunCommandWithStdIn(ctx context.Context, stdin string, args ...string) (*CliResult, error) {
	description := "azd " + strings.Join(args, " ") + " in " + cli.WorkingDirectory

	cmd := exec.CommandContext(ctx, cli.AzdLocation, args...)
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

func GetAzdLocation() string {
	_, b, _, _ := runtime.Caller(0)

	if runtime.GOOS == "windows" {
		return filepath.Join(filepath.Dir(b), "..", "..", "azd.exe")
	} else {
		return filepath.Join(filepath.Dir(b), "..", "..", "azd")
	}
}
