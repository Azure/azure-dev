// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

// Contains support for automating the use of the azd CLI

package azdcli

import (
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

type CLI struct {
	T                *testing.T
	WorkingDirectory string
	ConfigFilePath   string
	Env              []string
}

func NewCLI(t *testing.T) *CLI {
	return &CLI{
		T: t,
	}
}

func (cli *CLI) RunCommandWithStdIn(ctx context.Context, stdin string, args ...string) (string, error) {
	description := "azd " + strings.Join(args, " ") + " in " + cli.WorkingDirectory

	args = append(args, "--debug")
	cmd := exec.CommandContext(ctx, GetAzdLocation(), args...)
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

	out, err := cmd.CombinedOutput()

	// If there's no context error, we know the command completed (or error).
	for _, line := range strings.Split(string(out), "\n") {
		cli.T.Logf("[out] %s", line)
	}

	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(out), fmt.Errorf("command '%s' timed out: %w", description, err)
	}

	if errors.Is(ctx.Err(), context.Canceled) {
		// bubble up errors due to cancellation with their output, and let the caller
		// decide how to handle it.
		return string(out), ctx.Err()
	}

	if err != nil {
		return string(out), fmt.Errorf("command '%s' had non-zero exit code: %w", description, err)
	}

	return string(out), nil
}

func (cli *CLI) RunCommand(ctx context.Context, args ...string) (string, error) {
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
