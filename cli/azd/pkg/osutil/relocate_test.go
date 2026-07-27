// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package osutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/test/proctest"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if proctest.RunRequestedHelper() {
		return
	}

	os.Exit(m.Run())
}

// requireWindowsImageLock skips tests that depend on the one behavior only Windows has:
// a running executable's image file cannot be deleted, but can still be renamed.
// Unix unlinks a running binary without complaint, so there is nothing to relocate there.
func requireWindowsImageLock(t *testing.T) {
	t.Helper()

	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to delete the image file of a running executable")
	}
}

// startLockedExecutable runs a real executable out of dir, so dir contains a file the
// operating system will refuse to delete for as long as the process lives.
// It returns the path of the locked file and a function that stops the process.
func startLockedExecutable(t *testing.T, dir string) (string, func()) {
	t.Helper()

	handle := proctest.StartIn(t, dir, "azd-osutil-lock-helper", proctest.ModeIdle)
	handle.RequireImageLocked(t)

	return handle.Path, handle.Stop
}

// T19: a file that cannot be deleted is moved out of the directory being removed, which
// is what lets the directory itself go away.
func TestRelocateLocked_MovesFileOutOfDir(t *testing.T) {
	requireWindowsImageLock(t)

	base := t.TempDir()
	extensionDir := filepath.Join(base, "extension")
	trashDir := filepath.Join(base, ".trash")
	require.NoError(t, os.MkdirAll(extensionDir, PermissionDirectory))

	locked, _ := startLockedExecutable(t, extensionDir)

	relocated, err := relocateLockedFiles(extensionDir, trashDir)
	require.NoError(t, err)
	require.Equal(t, 1, relocated)

	require.NoFileExists(t, locked, "the locked file should no longer be inside the extension directory")

	entries, err := os.ReadDir(trashDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the locked file should have been moved into the trash directory")
	require.True(t, strings.HasPrefix(entries[0].Name(), filepath.Base(locked)),
		"the relocated entry should stay recognizable, got %q", entries[0].Name())
}

// T20: the whole point of the fallback. Removing a directory that holds a running
// executable must succeed instead of failing with a bare access denied.
func TestRemoveAllWithRelocation_LockedExe(t *testing.T) {
	requireWindowsImageLock(t)

	base := t.TempDir()
	extensionDir := filepath.Join(base, "extension")
	trashDir := filepath.Join(base, ".trash")
	require.NoError(t, os.MkdirAll(filepath.Join(extensionDir, "nested"), PermissionDirectory))
	require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "nested", "readme.md"), []byte("x"), 0600))

	_, stop := startLockedExecutable(t, extensionDir)

	require.NoError(t, RemoveAllWithRelocation(t.Context(), extensionDir, trashDir))
	require.NoDirExists(t, extensionDir, "the extension directory must be gone even though a process is running")

	// The relocated image is still held, so it stays in the trash until the process exits.
	entries, err := os.ReadDir(trashDir)
	require.NoError(t, err)
	require.Len(t, entries, 1)

	// Once the holder exits, a sweep clears the leftover.
	stop()
	require.Eventually(t, func() bool {
		SweepTrash(t.Context(), trashDir)

		_, err := os.Stat(trashDir)

		return os.IsNotExist(err)
	}, 30*time.Second, 200*time.Millisecond, "trash should be swept once the holding process exits")
}

// T21: nothing locked means nothing relocated. The common case must not create trash.
func TestRemoveAllWithRelocation_NoLocks(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	extensionDir := filepath.Join(base, "extension")
	trashDir := filepath.Join(base, ".trash")

	require.NoError(t, os.MkdirAll(filepath.Join(extensionDir, "nested"), PermissionDirectory))
	require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "a.txt"), []byte("a"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(extensionDir, "nested", "b.txt"), []byte("b"), 0600))

	require.NoError(t, RemoveAllWithRelocation(t.Context(), extensionDir, trashDir))
	require.NoDirExists(t, extensionDir)
	require.NoDirExists(t, trashDir, "an unlocked removal must not create a trash directory")
}

// Removing something that is already gone is a success, matching os.RemoveAll.
func TestRemoveAllWithRelocation_MissingPath(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	require.NoError(t, RemoveAllWithRelocation(
		t.Context(), filepath.Join(base, "absent"), filepath.Join(base, ".trash")))
}

// The trash has to be outside the directory being removed, otherwise relocating into it
// would never empty that directory. The mistake is rejected rather than half-performed.
func TestRemoveAllWithRelocation_RejectsTrashInsidePath(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	extensionDir := filepath.Join(base, "extension")
	require.NoError(t, os.MkdirAll(extensionDir, PermissionDirectory))

	err := RemoveAllWithRelocation(t.Context(), extensionDir, filepath.Join(extensionDir, ".trash"))
	require.ErrorContains(t, err, "must not live inside")

	err = RemoveAllWithRelocation(t.Context(), extensionDir, extensionDir)
	require.ErrorContains(t, err, "must not live inside")

	require.DirExists(t, extensionDir, "a rejected call must not remove anything")
}

func TestRemoveAllWithRelocation_RequiresPaths(t *testing.T) {
	t.Parallel()

	require.ErrorContains(t, RemoveAllWithRelocation(t.Context(), "", "trash"), "a path is required")
	require.ErrorContains(t,
		RemoveAllWithRelocation(t.Context(), t.TempDir(), ""), "a trash directory is required")
}

// T22: leftovers are cleaned up on a later run once whatever held them has exited.
func TestSweepTrash_RemovesReleasedEntries(t *testing.T) {
	t.Parallel()

	trashDir := filepath.Join(t.TempDir(), ".trash")
	require.NoError(t, os.MkdirAll(filepath.Join(trashDir, "nested"), PermissionDirectory))
	require.NoError(t, os.WriteFile(filepath.Join(trashDir, "old.exe"), []byte("x"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(trashDir, "nested", "deep.txt"), []byte("y"), 0600))

	SweepTrash(t.Context(), trashDir)

	require.NoDirExists(t, trashDir, "an emptied trash directory should remove itself")
}

// T23: an entry whose process is still running stays put, and that is not a failure.
func TestSweepTrash_SkipsLockedEntries(t *testing.T) {
	requireWindowsImageLock(t)

	base := t.TempDir()
	trashDir := filepath.Join(base, ".trash")
	require.NoError(t, os.MkdirAll(trashDir, PermissionDirectory))
	require.NoError(t, os.WriteFile(filepath.Join(trashDir, "released.txt"), []byte("x"), 0600))

	locked, stop := startLockedExecutable(t, trashDir)

	SweepTrash(t.Context(), trashDir)

	require.FileExists(t, locked, "a still-running executable must survive the sweep")
	require.NoFileExists(t, filepath.Join(trashDir, "released.txt"), "released entries should still be cleared")
	require.DirExists(t, trashDir, "the trash directory must remain while it still holds entries")

	stop()

	require.Eventually(t, func() bool {
		SweepTrash(t.Context(), trashDir)

		_, err := os.Stat(trashDir)

		return os.IsNotExist(err)
	}, 30*time.Second, 200*time.Millisecond, "the sweep should succeed once the holder exits")
}

// T24: the sweep is attached to commands that must not fail because of it, so it has no
// error to propagate by construction. These inputs would all be errors for os.RemoveAll.
func TestSweepTrash_BestEffortNeverErrors(t *testing.T) {
	t.Parallel()

	// No return value to check: the contract is enforced by the signature. What is
	// verified here is that none of these inputs panic or damage anything.
	SweepTrash(t.Context(), "")
	SweepTrash(t.Context(), filepath.Join(t.TempDir(), "never-created"))

	notADirectory := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(notADirectory, []byte("x"), 0600))
	SweepTrash(t.Context(), notADirectory)
	require.FileExists(t, notADirectory, "a non-directory must be left alone rather than deleted")
}

// linkDirectory creates a directory link at linkPath pointing at target, skipping the
// test when the platform will not allow one.
//
// Windows needs Developer Mode or elevation for a symlink but allows a junction with
// neither, and a junction is the more dangerous of the two here: filepath.EvalSymlinks
// refuses to resolve it while os.ReadDir and os.Rename follow it happily. Preferring the
// junction on Windows therefore keeps this coverage running on an ordinary CI agent and
// exercises the harder case.
func linkDirectory(t *testing.T, linkPath string, target string) {
	t.Helper()

	if runtime.GOOS == "windows" {
		//nolint:gosec // Fixed executable, arguments are test-controlled temp paths.
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", linkPath, target).CombinedOutput(); err != nil {
			t.Skipf("cannot create a directory junction on this machine: %v: %s", err, out)
		}

		return
	}

	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("cannot create a symlink on this machine: %v", err)
	}
}

func TestRequireRealDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	realDir := filepath.Join(dir, "real")
	require.NoError(t, os.Mkdir(realDir, PermissionDirectory))
	require.NoError(t, RequireRealDir(realDir), "a real directory must be accepted")

	// Nothing to redirect, so callers are free to create it.
	require.NoError(t, RequireRealDir(filepath.Join(dir, "missing")))

	file := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0600))
	require.ErrorIs(t, RequireRealDir(file), ErrLinkedDirectory)

	link := filepath.Join(dir, "link")
	linkDirectory(t, link, realDir)
	require.ErrorIs(t, RequireRealDir(link), ErrLinkedDirectory,
		"a directory link must be refused rather than resolved")
}

// A link planted where the trash directory belongs must not turn a routine sweep into a
// delete of whatever it points at. The sweep runs on every install, uninstall, and
// upgrade, including without --force, so this is the most reachable of the link paths.
func TestSweepTrash_RefusesLinkedDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	target := filepath.Join(root, "valuables")
	require.NoError(t, os.Mkdir(target, PermissionDirectory))

	keep := filepath.Join(target, "keep.txt")
	require.NoError(t, os.WriteFile(keep, []byte("x"), 0600))

	trashDir := filepath.Join(root, ".trash")
	linkDirectory(t, trashDir, target)

	SweepTrash(t.Context(), trashDir)

	require.FileExists(t, keep, "the link target's contents must survive the sweep")

	// Lstat rather than require.DirExists: a Windows junction reports IsDir false, so the
	// assertion is that the link entry itself is still there, not that it looks like a
	// directory.
	_, err := os.Lstat(trashDir)
	require.NoError(t, err, "the link itself must be left in place rather than removed")
}

// The trash directory is created lazily during relocation, so the same link check has to
// run there: creating it does not prove the name was not already a link.
func TestRelocateLocked_RefusesLinkedTrash(t *testing.T) {
	t.Parallel()
	requireWindowsImageLock(t)

	root := t.TempDir()

	source := filepath.Join(root, "extension")
	require.NoError(t, os.Mkdir(source, PermissionDirectory))
	_, stop := startLockedExecutable(t, source)

	defer stop()

	target := filepath.Join(root, "valuables")
	require.NoError(t, os.Mkdir(target, PermissionDirectory))

	trashDir := filepath.Join(root, ".trash")
	linkDirectory(t, trashDir, target)

	relocated, err := relocateLockedFiles(source, trashDir)

	require.ErrorIs(t, err, ErrLinkedDirectory)
	require.Zero(t, relocated, "no file may be moved into a linked trash directory")

	entries, readErr := os.ReadDir(target)
	require.NoError(t, readErr)
	require.Empty(t, entries, "nothing may be written through the link")
}

// Relocated names must not collide, or a file trashed today would silently overwrite one
// left behind by an earlier run that is still in use. They must also not collide across
// processes, because destination selection and the rename that follows it are separate
// operations that two azd processes can interleave.
func TestUniqueTrashPath(t *testing.T) {
	t.Parallel()

	trashDir := t.TempDir()

	seen := map[string]struct{}{}

	for range 100 {
		candidate := uniqueTrashPath(trashDir, "tool.exe")

		require.Equal(t, trashDir, filepath.Dir(candidate), "candidates must stay inside the trash directory")
		require.True(t, strings.HasPrefix(filepath.Base(candidate), "tool.exe."),
			"the original name should remain recognizable, got %q", candidate)
		require.Contains(t, filepath.Base(candidate), fmt.Sprintf(".%d.", os.Getpid()),
			"the name should carry this process's id so another process cannot pick it")

		_, duplicate := seen[candidate]
		require.False(t, duplicate, "uniqueTrashPath returned %q twice", candidate)
		seen[candidate] = struct{}{}
	}
}

// Two azd processes relocating the same base name at the same time must both succeed,
// and neither may overwrite the other's file.
func TestRelocateInto_ConcurrentRelocationsDoNotCollide(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	trashDir := filepath.Join(base, ".trash")
	require.NoError(t, os.MkdirAll(trashDir, PermissionDirectory))

	const concurrent = 8

	roots := make([]string, 0, concurrent)

	for i := range concurrent {
		root := filepath.Join(base, fmt.Sprintf("extension-%d", i))
		require.NoError(t, os.MkdirAll(root, PermissionDirectory))
		// Every root uses the same base name, which is what made the old
		// check-then-rename scheme hand two callers the same destination.
		require.NoError(t, os.WriteFile(filepath.Join(root, "tool.exe"), fmt.Appendf(nil, "payload-%d", i), 0600))
		roots = append(roots, root)
	}

	var wg sync.WaitGroup

	errs := make([]error, concurrent)

	for i, root := range roots {
		wg.Go(func() {
			errs[i] = relocateInto(filepath.Join(root, "tool.exe"), trashDir, "tool.exe")
		})
	}

	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "relocation %d should have succeeded", i)
	}

	entries, err := os.ReadDir(trashDir)
	require.NoError(t, err)
	require.Len(t, entries, concurrent, "every concurrent relocation needs its own destination")

	payloads := map[string]struct{}{}

	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(trashDir, entry.Name()))
		require.NoError(t, err)
		payloads[string(content)] = struct{}{}
	}

	require.Len(t, payloads, concurrent, "no relocation may have overwritten another's file")
}
