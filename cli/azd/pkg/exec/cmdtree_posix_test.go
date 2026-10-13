// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build !windows

package exec

import (
	"bufio"
	"os"
	"os/exec"
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
