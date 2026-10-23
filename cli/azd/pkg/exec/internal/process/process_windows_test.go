// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package process

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

const windowsProcessHelperStartedEnv = "AZD_WINDOWS_PROCESS_HELPER_STARTED"

func TestWindowsProcessContainmentHelper(t *testing.T) {
	startedPath := os.Getenv(windowsProcessHelperStartedEnv)
	if startedPath == "" {
		return
	}

	//nolint:gosec // G703: path is supplied by the parent test process
	require.NoError(t, os.WriteFile(startedPath, []byte("started"), 0o600))
}

func TestConfigureWindowsProcessGroup(t *testing.T) {
	for _, test := range []struct {
		name              string
		forwardInterrupts bool
		wantProcessGroup  bool
		creationFlags     uint32
		wantResume        bool
	}{
		{
			name:             "isolated process group",
			wantProcessGroup: true,
			wantResume:       true,
		},
		{
			name:              "forward interrupts",
			forwardInterrupts: true,
			wantResume:        true,
		},
		{
			name:             "caller requested suspension",
			wantProcessGroup: true,
			creationFlags:    windows.CREATE_SUSPENDED,
			wantResume:       false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec
			cmd.SysProcAttr = &syscall.SysProcAttr{
				CreationFlags: windows.CREATE_NO_WINDOW | test.creationFlags,
			}
			tree := New(t.Context(), cmd, Options{
				ForwardInterrupts: test.forwardInterrupts,
			})

			configure(tree)

			require.Equal(
				t,
				test.wantProcessGroup,
				cmd.SysProcAttr.CreationFlags&syscall.CREATE_NEW_PROCESS_GROUP != 0,
			)
			require.NotZero(t, cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW)
			require.NotZero(t, cmd.SysProcAttr.CreationFlags&windows.CREATE_SUSPENDED)
			require.Equal(t, test.wantResume, tree.platform.resumeAfterJobAssignment)
		})
	}
}

func TestWindowsProcessDoesNotRunBeforeJobAssignment(t *testing.T) {
	startedPath := filepath.Join(t.TempDir(), "started")
	cmd := exec.Command( //nolint:gosec
		os.Args[0],
		"-test.run=^TestWindowsProcessContainmentHelper$",
	)
	cmd.Env = append(os.Environ(), windowsProcessHelperStartedEnv+"="+startedPath)
	tree := New(t.Context(), cmd, Options{})

	configure(tree)
	require.NoError(t, cmd.Start())
	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	require.Never(t, func() bool {
		_, err := os.Stat(startedPath)
		return err == nil
	}, 250*time.Millisecond, 10*time.Millisecond)

	require.NoError(t, finishStart(tree))
	err := tree.Wait()
	waited = true
	require.NoError(t, err)
	require.FileExists(t, startedPath)
}
