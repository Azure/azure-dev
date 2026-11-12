// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ostest

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// Symlink creates a symbolic link, skipping only when Windows denies the symlink privilege.
func Symlink(t *testing.T, target, link string) {
	t.Helper()
	err := os.Symlink(target, link)
	// ERROR_PRIVILEGE_NOT_HELD: Windows requires Developer Mode or the symlink privilege.
	if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
		t.Skipf("symbolic links are not available: %v", err)
	}
	require.NoError(t, err)
}

// DirectoryLink creates a directory symlink, or a junction on Windows without requiring symlink privileges.
func DirectoryLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		Symlink(t, target, link)
		return
	}
	// #nosec G204 -- Arguments are paths created by tests, not external input.
	output, err := exec.CommandContext(t.Context(), "cmd.exe", "/c", "mklink", "/J", link, target).CombinedOutput()
	require.NoError(t, err, "%s", output)
}
