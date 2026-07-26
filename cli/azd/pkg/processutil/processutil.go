// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package processutil discovers and stops operating system processes whose executable
// image lives inside a caller-supplied directory.
//
// azd needs this when it has to replace or delete files that a running process holds
// open, most visibly when upgrading or uninstalling an extension that is still running.
//
// Discovery is always scoped to a directory. A process becomes a candidate only when its
// executable is contained within the directory the caller names. Because callers use the
// result to terminate processes, that containment check is the safety property this
// package is built around, and it is validated before any enumeration happens.
package processutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

const (
	// DefaultGracePeriod is how long Terminate waits for a process to exit on its own
	// after a graceful stop is requested, before escalating to a forceful stop.
	DefaultGracePeriod = 5 * time.Second

	// forceConfirmWindow bounds how long Terminate waits for a process to disappear
	// after a forceful stop. It is deliberately short: the kill has already been issued,
	// so this only covers the kernel reaping the process.
	forceConfirmWindow = 2 * time.Second

	// pollInterval is how often Terminate re-checks whether a process has exited.
	pollInterval = 100 * time.Millisecond
)

var (
	// ErrEmptyDirectory is returned when no directory is supplied to scope discovery.
	ErrEmptyDirectory = errors.New("a directory is required to scope process discovery")

	// ErrRootDirectory is returned when the supplied directory is a filesystem root.
	// Containment against a root matches every process on the machine, so it is refused
	// rather than silently honored.
	ErrRootDirectory = errors.New("refusing to scope process discovery to a filesystem root")

	// ErrInvalidPID is returned when a non-positive process ID is supplied.
	ErrInvalidPID = errors.New("process id must be greater than zero")

	// ErrProcessOutOfScope is returned when the caller asks to terminate a process whose
	// recorded executable does not sit inside the supplied scope. Scope is the security
	// boundary for termination, so a process that cannot be shown to be inside it is
	// refused rather than trusted.
	ErrProcessOutOfScope = errors.New("refusing to terminate a process outside the requested directory")
)

// ProcessInfo describes a running process discovered by this package.
type ProcessInfo struct {
	// PID is the operating system process identifier.
	PID int
	// Name is the executable's base name, used for display.
	Name string
	// Executable is the full path to the process's executable image.
	Executable string
}

// String renders the process for user-facing messages, for example "myext.exe (PID 1234)".
func (p ProcessInfo) String() string {
	name := p.Name
	if name == "" {
		name = "unknown"
	}

	return fmt.Sprintf("%s (PID %d)", name, p.PID)
}

// Describe renders processes as a comma separated list for error and status messages.
func Describe(processes []ProcessInfo) string {
	parts := make([]string, 0, len(processes))
	for _, process := range processes {
		parts = append(parts, process.String())
	}

	return strings.Join(parts, ", ")
}

// FindByExecutableDir returns every running process whose executable image is contained
// within dir. The calling process is never returned.
//
// dir must be a non-empty, non-root path. Both constraints are refused rather than
// silently widened, because callers terminate what this function returns and a root scope
// would match unrelated processes.
//
// Enumeration is best effort. Processes that cannot be inspected, because they exited
// mid-scan or belong to another user, are skipped instead of failing the whole call.
func FindByExecutableDir(ctx context.Context, dir string) ([]ProcessInfo, error) {
	scope, err := normalizeScope(dir)
	if err != nil {
		return nil, err
	}

	candidates, err := enumerateProcesses(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate processes: %w", err)
	}

	self := os.Getpid()
	matches := make([]ProcessInfo, 0, len(candidates))

	for _, candidate := range candidates {
		if candidate.PID <= 0 || candidate.PID == self || candidate.Executable == "" {
			continue
		}

		if !executableInScope(scope, candidate.Executable) {
			continue
		}

		if candidate.Name == "" {
			candidate.Name = filepath.Base(candidate.Executable)
		}

		matches = append(matches, candidate)
	}

	return matches, nil
}

// Terminate stops the process described by process, which must still be running out of
// scope for the stop to proceed.
//
// It reports whether a signal was actually delivered. A process that had already exited
// is not an error, but it is also not something azd stopped, and callers report those two
// outcomes differently to the user.
//
// It asks the process to stop, waits up to grace for it to exit on its own, then forces
// it to stop if it is still running.
//
// The graceful step is platform dependent. On Unix the process is sent SIGTERM. Windows
// has no safe general purpose graceful signal for a process azd does not own, so that
// step is skipped there and the process is stopped directly.
//
// Scope is checked twice, for two different reasons.
//
// The supplied process is checked against scope up front. A caller that asks to stop
// something it cannot show is inside the scope is refused, which keeps a malformed or
// attacker-influenced ProcessInfo from reaching a kill.
//
// Scope is then re-checked against live operating system state immediately before each
// signal. Discovery and termination are separate moments, and a PID is only a number: the
// process can exit in between and the operating system can hand the same number to
// something unrelated. Without the re-check a reused PID would be signalled on the
// strength of a stale match.
func Terminate(ctx context.Context, process ProcessInfo, scope string, grace time.Duration) (bool, error) {
	if process.PID <= 0 {
		return false, fmt.Errorf("%w: %d", ErrInvalidPID, process.PID)
	}

	if process.PID == os.Getpid() {
		return false, fmt.Errorf("refusing to terminate the current process (PID %d)", process.PID)
	}

	normalized, err := normalizeScope(scope)
	if err != nil {
		return false, err
	}

	if process.Executable == "" || !executableInScope(normalized, process.Executable) {
		return false, fmt.Errorf("%w: %s is not inside %s", ErrProcessOutOfScope, process.String(), normalized)
	}

	live, err := runningInScope(ctx, process.PID, normalized)
	if err != nil {
		return false, err
	}

	if !live {
		return false, nil
	}

	signalled := false

	if err := signalGraceful(process.PID); err == nil {
		signalled = true

		if waitForExit(ctx, process.PID, grace) {
			return true, nil
		}
	}

	// The grace period above is the widest part of the window, so re-check before the
	// forced stop rather than trusting the check from before the wait.
	live, err = runningInScope(ctx, process.PID, normalized)
	if err != nil {
		return signalled, err
	}

	if !live {
		return signalled, nil
	}

	if err := forceKill(process.PID); err != nil {
		// Losing the race against a process that exited on its own is a success, not a
		// failure, so re-check before reporting the error.
		if !processRunning(process.PID) {
			return signalled, nil
		}

		return signalled, fmt.Errorf("failed to stop process %d: %w", process.PID, err)
	}

	if !waitForExit(ctx, process.PID, forceConfirmWindow) {
		return true, fmt.Errorf("process %d did not exit after a forced stop", process.PID)
	}

	return true, nil
}

// runningInScope reports whether pid currently maps to a live process whose executable
// image sits inside scope, which scope must already be normalized.
//
// An enumeration failure is returned rather than reported as "not running". Collapsing it
// would make an unverifiable target look like one that had already exited, and --force
// would claim success without having stopped anything.
func runningInScope(ctx context.Context, pid int, scope string) (bool, error) {
	candidates, err := enumerateProcesses(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to enumerate processes: %w", err)
	}

	for _, candidate := range candidates {
		if candidate.PID != pid {
			continue
		}

		return candidate.Executable != "" && executableInScope(scope, candidate.Executable), nil
	}

	return false, nil
}

// normalizeScope validates and canonicalizes the directory used to scope discovery.
func normalizeScope(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", ErrEmptyDirectory
	}

	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %q: %w", dir, err)
	}

	// Refuse a scope whose final component is a symlink or junction. EvalSymlinks below
	// would resolve it and hand back the link target, silently widening the set of
	// processes a caller is willing to terminate to whatever the link points at. Callers
	// build scopes from directories they own, so a link there is never legitimate.
	if err := osutil.RequireRealDir(absolute); err != nil {
		return "", err
	}

	// Resolve symlinks so a linked install directory still matches the real executable
	// paths the operating system reports. A directory that does not exist has nothing to
	// resolve, so the absolute form is kept and simply matches nothing.
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}

	absolute = filepath.Clean(absolute)

	if isFilesystemRoot(absolute) {
		return "", fmt.Errorf("%w: %q", ErrRootDirectory, absolute)
	}

	return absolute, nil
}

// isFilesystemRoot reports whether p is a filesystem, volume, or UNC share root.
// A root is its own parent, which holds for "/", "C:\", and "\\server\share".
func isFilesystemRoot(p string) bool {
	cleaned := filepath.Clean(p)

	return filepath.Dir(cleaned) == cleaned
}

// executableInScope reports whether an executable path falls inside the scope directory.
// Both the reported path and its symlink-resolved form are checked, because a relocated
// or unlinked binary may no longer resolve while its process keeps running.
func executableInScope(scope, executable string) bool {
	absolute, err := filepath.Abs(executable)
	if err != nil {
		return false
	}

	if osutil.IsPathContained(scope, filepath.Clean(absolute)) {
		return true
	}

	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return false
	}

	return osutil.IsPathContained(scope, filepath.Clean(resolved))
}

// waitForExit polls until the process exits, the timeout elapses, or ctx is cancelled.
// It reports whether the process is gone.
func waitForExit(ctx context.Context, pid int, timeout time.Duration) bool {
	if timeout <= 0 {
		return !processRunning(pid)
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		if !processRunning(pid) {
			return true
		}

		select {
		case <-ctx.Done():
			return !processRunning(pid)
		case <-deadline.C:
			return !processRunning(pid)
		case <-ticker.C:
		}
	}
}
