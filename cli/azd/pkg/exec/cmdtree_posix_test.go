// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package exec

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCmdTree_KillInteractiveProcess(t *testing.T) {
	cmd := &CmdTree{
		CmdTreeOptions: CmdTreeOptions{Interactive: true},
		Cmd:            exec.Command("/bin/sh", "-c", "trap '' INT; echo ready; while :; do :; done"),
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	_, err = bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	select {
	case err := <-waitDone:
		t.Fatalf("interactive process exited after ignored interrupt: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cmd.Kill()
	select {
	case err := <-waitDone:
		require.Error(t, err)
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
		<-waitDone
		t.Fatal("interactive process did not stop after Kill")
	}
}

func TestCmdTree_ContextCancellationKillsInteractiveDescendants(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cmd := &CmdTree{
		CmdTreeOptions: CmdTreeOptions{Interactive: true},
		Cmd: exec.CommandContext(
			ctx,
			"/bin/sh",
			"-c",
			`trap '' INT; /bin/sh -c 'trap "" INT; echo "$$"; while :; do :; done' & while :; do :; done`,
		),
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	childLine, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(childLine))
	require.NoError(t, err)

	waitDone := make(chan error, 1)
	waitCompleted := false
	go func() {
		waitDone <- cmd.Wait()
	}()
	t.Cleanup(func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		if !waitCompleted {
			select {
			case <-waitDone:
			case <-time.After(time.Second):
			}
		}
	})

	require.True(t, posixProcessRunning(childPID))
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	require.NoError(t, syscall.Kill(childPID, syscall.SIGINT))
	time.Sleep(50 * time.Millisecond)
	require.True(t, posixProcessRunning(childPID))

	cancel()
	select {
	case err := <-waitDone:
		waitCompleted = true
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("interactive process did not stop after context cancellation")
	}

	require.Eventually(t, func() bool {
		return !posixProcessRunning(childPID)
	}, time.Second, 10*time.Millisecond)
}

func TestCmdTree_CleanupKillsReparentedInteractiveDescendants(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cmd := &CmdTree{
		CmdTreeOptions: CmdTreeOptions{Interactive: true},
		Cmd: exec.CommandContext(
			ctx,
			"/bin/sh",
			"-c",
			`trap 'exit 130' INT; /bin/sh -c 'trap "" INT; echo "$$"; while :; do :; done' & wait`,
		),
	}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	childLine, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(childLine))
	require.NoError(t, err)

	waitDone := make(chan error, 1)
	waitCompleted := false
	go func() {
		waitDone <- waitForProcess(ctx, cmd.Wait, cmd.Kill)
	}()
	t.Cleanup(func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		if !waitCompleted {
			select {
			case <-waitDone:
			case <-time.After(time.Second):
			}
		}
	})

	require.True(t, posixProcessRunning(childPID))
	require.NoError(t, syscall.Kill(childPID, syscall.SIGINT))
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	select {
	case err := <-waitDone:
		waitCompleted = true
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("interactive parent did not stop after interrupt")
	}

	require.Eventually(t, func() bool {
		return !posixProcessRunning(childPID)
	}, time.Second, 10*time.Millisecond)
}

func posixProcessRunning(pid int) bool {
	output, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // pid is numeric
	if err != nil {
		return false
	}
	status := strings.TrimSpace(string(output))
	return status != "" && !strings.HasPrefix(status, "Z")
}
