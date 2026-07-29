// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package processutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/require"
)

// T1: an empty scope would match every process on the machine, so it must be refused.
func TestFindByExecutableDir_RejectsEmptyDir(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"", "   ", "\t"} {
		found, err := FindByExecutableDir(t.Context(), dir)
		require.ErrorIs(t, err, ErrEmptyDirectory)
		require.Nil(t, found)
	}
}

// T2: the Unix filesystem root contains everything, so it must be refused.
func TestFindByExecutableDir_RejectsUnixRoot(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("posix root semantics do not apply on Windows")
	}

	for _, dir := range []string{"/", "//", "/.", "/usr/.."} {
		found, err := FindByExecutableDir(t.Context(), dir)
		require.ErrorIsf(t, err, ErrRootDirectory, "expected %q to be rejected as a root", dir)
		require.Nil(t, found)
	}
}

// T3: a Windows volume root contains everything on that volume, so it must be refused.
func TestFindByExecutableDir_RejectsWindowsRoot(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "windows" {
		t.Skip("drive roots only exist on Windows")
	}

	volume := filepath.VolumeName(os.Getenv("SystemDrive"))
	if volume == "" {
		volume = "C:"
	}

	for _, dir := range []string{volume + `\`, volume + `\.`, volume + `\Windows\..`} {
		found, err := FindByExecutableDir(t.Context(), dir)
		require.ErrorIsf(t, err, ErrRootDirectory, "expected %q to be rejected as a root", dir)
		require.Nil(t, found)
	}
}

// T4: a UNC share root is a root like any other and must be refused too.
func TestFindByExecutableDir_RejectsUNCRoot(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "windows" {
		t.Skip("UNC paths only exist on Windows")
	}

	// The share need not exist. Rejection is a path-shape decision made before any
	// filesystem or process access, which is what keeps the guard trustworthy.
	found, err := FindByExecutableDir(t.Context(), `\\testserver\testshare`)
	require.ErrorIs(t, err, ErrRootDirectory)
	require.Nil(t, found)
}

// T5: azd must never hand back its own PID, or --force would terminate the CLI itself.
func TestFindByExecutableDir_ExcludesSelf(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	require.NoError(t, err)

	found, err := FindByExecutableDir(t.Context(), filepath.Dir(executable))
	require.NoError(t, err)

	for _, process := range found {
		require.NotEqual(t, os.Getpid(), process.PID, "discovery returned the current process")
	}
}

// T6: a scope reached through a symlinked ancestor must still resolve, otherwise
// containment would never match on macOS, where the temp directory sits under /var and
// the OS reports executables under /private/var.
func TestFindByExecutableDir_ResolvesSymlinkedAncestors(t *testing.T) {
	t.Parallel()

	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")

	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}

	child := "child"
	require.NoError(t, os.Mkdir(filepath.Join(real, child), 0755))

	resolvedReal, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)

	// The final component is a real directory; only the path used to reach it is linked.
	scope, err := normalizeScope(filepath.Join(link, child))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(filepath.Clean(resolvedReal), child), scope)
}

// A scope whose own final component is a link must be refused rather than resolved.
// Resolving it would hand back the link target, so anything able to plant a link where
// azd expects an install directory could widen a scoped termination to whatever it
// points at. azd creates these directories itself, so a link there is never legitimate.
func TestFindByExecutableDir_RejectsLinkedScope(t *testing.T) {
	t.Parallel()

	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")

	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}

	_, err := normalizeScope(link)
	require.ErrorIs(t, err, osutil.ErrLinkedDirectory)

	_, err = FindByExecutableDir(t.Context(), link)
	require.ErrorIs(t, err, osutil.ErrLinkedDirectory,
		"discovery must refuse a linked scope, not just normalization")
}

// T7: a relative scope must become absolute before comparison, since process
// executables are always reported as absolute paths.
func TestFindByExecutableDir_ResolvesRelativePath(t *testing.T) {
	base := t.TempDir()
	child := filepath.Join(base, "child")
	require.NoError(t, os.MkdirAll(child, 0755))

	t.Chdir(base)

	scope, err := normalizeScope("child")
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(scope), "scope %q should be absolute", scope)

	resolvedChild, err := filepath.EvalSymlinks(child)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(resolvedChild), scope)
}

// T8: "ext" must not match "ext-backup". A plain string prefix test would, which is
// exactly the class of bug that turns a scoped stop into an unscoped one.
func TestFindByExecutableDir_RejectsPrefixSibling(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	scope := filepath.Join(base, "ext")
	sibling := filepath.Join(base, "ext-backup")

	require.NoError(t, os.MkdirAll(scope, 0755))
	require.NoError(t, os.MkdirAll(sibling, 0755))

	normalized, err := normalizeScope(scope)
	require.NoError(t, err)

	require.False(t, executableInScope(normalized, filepath.Join(sibling, "tool.exe")),
		"a sibling sharing a name prefix must not be considered contained")
	require.True(t, executableInScope(normalized, filepath.Join(scope, "tool.exe")))
}

// T9: containment must follow the filesystem it is running on. Windows matches
// case-insensitively, Linux does not.
func TestFindByExecutableDir_CaseSensitivityByPlatform(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	scope := filepath.Join(base, "MyExtension")
	require.NoError(t, os.MkdirAll(scope, 0755))

	normalized, err := normalizeScope(scope)
	require.NoError(t, err)

	sameCase := filepath.Join(normalized, "tool.exe")
	require.True(t, executableInScope(normalized, sameCase))

	differentCase := filepath.Join(strings.ToLower(normalized), "tool.exe")

	if runtime.GOOS == "windows" {
		require.True(t, executableInScope(normalized, differentCase),
			"Windows paths are case-insensitive, so a lowercased path must still match")

		return
	}

	// A case-sensitive comparison. strings.EqualFold here would compare the path against
	// its own lowercase form case-insensitively, which is true for every input, so the
	// assertion below would never run.
	if normalized == strings.ToLower(normalized) {
		t.Skip("temp directory has no case variation to exercise")
	}

	require.False(t, executableInScope(normalized, differentCase),
		"case-sensitive filesystems must treat a differently cased path as a different path")
}

// A literal backslash is a legal filename character on Unix. osutil.IsPathContained
// rewrites backslashes to the OS separator, which is correct for validating a path a user
// typed but wrong for an OS-reported executable image: it would clean an out-of-scope
// binary into scope and make it a termination candidate. Containment must stay host-native.
func TestFindByExecutableDir_UnixBackslashIsNotASeparator(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backslash is a real separator on Windows, so there is nothing to confuse")
	}

	t.Parallel()

	base := t.TempDir()
	scope := filepath.Join(base, "extensions", "demo")
	require.NoError(t, os.MkdirAll(scope, 0755))

	normalized, err := normalizeScope(scope)
	require.NoError(t, err)

	// A single file directly under base whose name literally contains backslashes.
	// Rewriting them as separators cleans it to <base>/extensions/demo/tool, in scope.
	outside := filepath.Join(base, `other\..\extensions\demo\tool`)

	require.False(t, executableInScope(normalized, outside),
		"a backslash in a Unix filename must not be reinterpreted as a path separator")
}

// Containment is the safety property the package is built around, so an escaping path must
// be refused no matter how it is spelled.
func TestPathContained(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	scope := filepath.Join(base, "extensions", "demo")

	require.True(t, pathContained(scope, scope), "a scope contains itself")
	require.True(t, pathContained(scope, filepath.Join(scope, "tool")))
	require.True(t, pathContained(scope, filepath.Join(scope, "nested", "tool")))

	// A child whose name merely starts with ".." is inside the scope. This is why the
	// escape check tests for ".." followed by a separator rather than a bare prefix.
	require.True(t, pathContained(scope, filepath.Join(scope, "..cache", "tool")))

	// The immediate parent is the only input that relativizes to exactly "..", so it is
	// the only one that exercises the equality half of the escape check. Without it,
	// dropping that clause would widen the termination boundary to the parent directory
	// with every other assertion here still passing.
	require.False(t, pathContained(scope, filepath.Join(base, "extensions")),
		"the immediate parent of the scope is not contained by it")

	require.False(t, pathContained(scope, base))
	require.False(t, pathContained(scope, filepath.Join(base, "extensions", "demo-backup", "tool")))
	require.False(t, pathContained(scope, filepath.Join(scope, "..", "other", "tool")))
}

// T10: a directory that was never created is a normal state (nothing installed there).
// It must yield no matches rather than an error or a panic.
func TestFindByExecutableDir_MissingDir(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does", "not", "exist")

	found, err := FindByExecutableDir(t.Context(), missing)
	require.NoError(t, err)
	require.Empty(t, found)
}

// isFilesystemRoot is the guard T2-T4 depend on, so its contract is pinned directly.
func TestIsFilesystemRoot(t *testing.T) {
	t.Parallel()

	require.False(t, isFilesystemRoot(t.TempDir()))

	if runtime.GOOS == "windows" {
		require.True(t, isFilesystemRoot(`C:\`))
		require.True(t, isFilesystemRoot(`\\server\share`))
		require.False(t, isFilesystemRoot(`C:\Users`))

		return
	}

	require.True(t, isFilesystemRoot("/"))
	require.False(t, isFilesystemRoot("/usr"))
}

// Terminate must refuse the current process outright, before any platform call.
func TestTerminate_RefusesSelf(t *testing.T) {
	t.Parallel()

	self, err := os.Executable()
	require.NoError(t, err)

	process := ProcessInfo{PID: os.Getpid(), Name: filepath.Base(self), Executable: self}
	_, err = Terminate(t.Context(), process, filepath.Dir(self), DefaultGracePeriod)
	require.ErrorContains(t, err, "refusing to terminate the current process")
}

// A non-positive PID has platform-specific meaning (process group, every process),
// so it must be rejected rather than passed through.
func TestTerminate_RejectsInvalidPID(t *testing.T) {
	t.Parallel()

	for _, pid := range []int{0, -1, -1000} {
		process := ProcessInfo{PID: pid, Executable: filepath.Join(t.TempDir(), "ext.exe")}
		_, err := Terminate(t.Context(), process, t.TempDir(), DefaultGracePeriod)
		require.ErrorIs(t, err, ErrInvalidPID)
	}
}

// The scope is the security boundary for --force. A process whose recorded executable
// sits outside the scope must never be signalled, no matter how it was discovered.
func TestTerminate_RejectsProcessOutsideScope(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scope := filepath.Join(root, "extension")
	require.NoError(t, os.MkdirAll(scope, 0o755))

	outside := filepath.Join(root, "elsewhere", "other.exe")
	process := ProcessInfo{PID: 999999, Name: "other.exe", Executable: outside}

	_, err := Terminate(t.Context(), process, scope, DefaultGracePeriod)
	require.ErrorIs(t, err, ErrProcessOutOfScope)
}

// An empty scope would make every containment check trivially pass, which would turn
// --force into an unbounded kill. It must be refused.
func TestTerminate_RejectsEmptyScope(t *testing.T) {
	t.Parallel()

	process := ProcessInfo{PID: 999999, Name: "ext.exe", Executable: filepath.Join(t.TempDir(), "ext.exe")}
	_, err := Terminate(t.Context(), process, "", DefaultGracePeriod)
	require.ErrorIs(t, err, ErrEmptyDirectory)
}

// A process with no recorded executable cannot be proven in scope, so it must be refused
// rather than trusted.
func TestTerminate_RejectsUnknownExecutable(t *testing.T) {
	t.Parallel()

	process := ProcessInfo{PID: 999999, Name: "ext.exe"}
	_, err := Terminate(t.Context(), process, t.TempDir(), DefaultGracePeriod)
	require.ErrorIs(t, err, ErrProcessOutOfScope)
}

// ProcessInfo renders into user-facing errors, so its formatting is part of the contract.
func TestProcessInfoString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "myext.exe (PID 42)", ProcessInfo{PID: 42, Name: "myext.exe"}.String())
	require.Equal(t, "unknown (PID 7)", ProcessInfo{PID: 7}.String())
}

func TestDescribe(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", Describe(nil))
	require.Equal(t, "a (PID 1)", Describe([]ProcessInfo{{PID: 1, Name: "a"}}))
	require.Equal(t, "a (PID 1), b (PID 2)", Describe([]ProcessInfo{
		{PID: 1, Name: "a"},
		{PID: 2, Name: "b"},
	}))
}

// Error messages name the binary being held open, and several instances of one extension
// normally share it, so the path is listed once rather than once per PID.
func TestDescribeExecutables(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", DescribeExecutables(nil))
	require.Equal(t, "", DescribeExecutables([]ProcessInfo{{PID: 1, Name: "a"}}),
		"a process with no recorded image contributes no path")

	require.Equal(t, filepath.Join("x", "a"), DescribeExecutables([]ProcessInfo{
		{PID: 1, Name: "a", Executable: filepath.Join("x", "a")},
		{PID: 2, Name: "a", Executable: filepath.Join("x", "a")},
	}), "watch-mode instances sharing one binary must not repeat its path")

	require.Equal(t,
		filepath.Join("x", "a")+", "+filepath.Join("x", "b"),
		DescribeExecutables([]ProcessInfo{
			{PID: 1, Name: "a", Executable: filepath.Join("x", "a")},
			{PID: 2, Name: "b", Executable: filepath.Join("x", "b")},
			{PID: 3, Name: "a", Executable: filepath.Join("x", "a")},
		}), "distinct binaries are listed in the order first seen")
}
