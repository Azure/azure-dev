// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A location is the directory before anything is written and the configuration
// file once it exists, and Stat could only tell those apart while the file was
// there. A recorded or $ref-declared configuration that had since been deleted
// therefore read as a directory: `init` wrote <path>/azure.eval.yaml under it
// while the wiring still pointed at <path>, so `azd up` deployed neither.
func TestADeletedConfigurationIsStillAFileName(t *testing.T) {
	dir := t.TempDir()

	for _, base := range []string{EvalConfigBase, LegacyEvalConfigBase} {
		gone := filepath.Join(dir, base)

		assert.Equal(t, dir, EvalDirOf(gone),
			"%s names a file whether or not it is on disk", base)
	}
}

// A directory that does not exist yet is still a directory: that is what `init`
// is handed before it scaffolds anything.
func TestAnAbsentDirectoryIsStillADirectory(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "evals")

	assert.Equal(t, absent, EvalDirOf(absent),
		"a bare directory name must not be mistaken for a configuration file")
}

// The lock path is chmodded and then locked, and both follow a symbolic link.
// A link committed to a repository would choose which file the permission
// repair widens to 0666.
func TestALockPathThatIsNotARegularFileIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link needs elevation on Windows")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "secret")
	require.NoError(t, os.WriteFile(target, []byte("private"), 0o600))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, evalConfigLockName)))

	unlock, err := LockEvalConfig(t.Context(), dir)
	if unlock != nil {
		unlock()
	}

	require.Error(t, err, "a symbolic link is not a lock file")
	assert.Contains(t, err.Error(), "not a regular file")

	info, statErr := os.Stat(target)
	require.NoError(t, statErr)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"the link's target must not have been widened")
}
