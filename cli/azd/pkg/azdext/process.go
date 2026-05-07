// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// ---------------------------------------------------------------------------
// Cross-platform process detection
// ---------------------------------------------------------------------------

// ProcessInfo contains information about a running process.
type ProcessInfo struct {
	// PID is the process identifier.
	PID int
	// Name is the process name (executable basename without extension).
	Name string
	// Executable is the full path to the process executable, if available.
	Executable string
	// Running is true if the process was found and appears to be alive.
	Running bool
}

// IsProcessRunning checks whether a process with the given PID exists and
// is still running.
//
// Platform behavior:
//   - Unix (Linux/macOS): Sends signal 0 to the process. If the process
//     exists (even if owned by another user), this returns true. If the
//     process does not exist, it returns false. This does NOT verify that
//     the process is the expected one (PID reuse is possible).
//   - Windows: Opens the process with PROCESS_QUERY_LIMITED_INFORMATION
//     access and checks the exit code. If the process handle is valid and
//     the exit code is STILL_ACTIVE, returns true.
//
// Note: PID reuse can cause false positives on all platforms. For critical
// use cases, combine PID checks with process name verification using
// [GetProcessInfo].
//
// Returns false if the PID is invalid (≤ 0).
func IsProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	return isProcessRunningOS(pid)
}

// GetProcessInfo retrieves information about the process with the given PID.
//
// Platform behavior:
//   - Linux: Reads /proc/<pid>/comm, /proc/<pid>/exe.
//   - macOS: Uses ps(1) to query process info.
//   - Windows: Uses QueryFullProcessImageName via Windows API.
//
// Returns a [ProcessInfo] with Running=false if the process does not exist
// or cannot be queried (e.g., insufficient permissions).
func GetProcessInfo(pid int) ProcessInfo {
	if pid <= 0 {
		return ProcessInfo{PID: pid, Running: false}
	}
	return getProcessInfoOS(pid)
}

// CurrentProcessInfo returns [ProcessInfo] for the current process.
func CurrentProcessInfo() ProcessInfo {
	pid := os.Getpid()
	exe, _ := os.Executable()

	name := ""
	if exe != "" {
		name = extractBaseName(exe)
	}

	return ProcessInfo{
		PID:        pid,
		Name:       name,
		Executable: exe,
		Running:    true,
	}
}

// ParentProcessInfo returns [ProcessInfo] for the parent of the current
// process.
//
// Platform behavior:
//   - All platforms: Uses os.Getppid() to obtain the parent PID, then
//     delegates to [GetProcessInfo].
//   - On orphaned processes (parent PID = 1 on Unix), the returned info
//     describes the init/launchd process.
func ParentProcessInfo() ProcessInfo {
	return GetProcessInfo(os.Getppid())
}

// FindProcessByName searches for running processes with the given name.
// The search is case-insensitive and matches the executable basename
// (without file extension on Windows).
//
// Platform behavior:
//   - Linux: Scans /proc/*/comm.
//   - macOS: Uses ps(1) to list processes.
//   - Windows: Uses CreateToolhelp32Snapshot to enumerate processes.
//
// Returns a slice of matching [ProcessInfo]. If no processes are found,
// returns an empty (non-nil) slice.
//
// This function is best-effort: some processes may be inaccessible due to
// permissions.
func FindProcessByName(name string) []ProcessInfo {
	if name == "" {
		return []ProcessInfo{}
	}
	return findProcessByNameOS(name)
}

// ProcessEnvironment describes the process execution context for diagnostics.
type ProcessEnvironment struct {
	// PID is the current process ID.
	PID int
	// PPID is the parent process ID.
	PPID int
	// Executable is the current process executable path.
	Executable string
	// WorkingDir is the current working directory.
	WorkingDir string
	// OS is the operating system (runtime.GOOS).
	OS string
	// Arch is the CPU architecture (runtime.GOARCH).
	Arch string
	// NumCPU is the number of logical CPUs available.
	NumCPU int
}

// GetProcessEnvironment collects process execution context useful for
// diagnostics, logging, and support information.
func GetProcessEnvironment() ProcessEnvironment {
	exe, _ := os.Executable()
	cwd, _ := os.Getwd()

	return ProcessEnvironment{
		PID:        os.Getpid(),
		PPID:       os.Getppid(),
		Executable: exe,
		WorkingDir: cwd,
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		NumCPU:     runtime.NumCPU(),
	}
}

// String returns a human-readable summary of the process environment.
func (pe ProcessEnvironment) String() string {
	return fmt.Sprintf("pid=%d ppid=%d os=%s arch=%s cpus=%d cwd=%s exe=%s",
		pe.PID, pe.PPID, pe.OS, pe.Arch, pe.NumCPU, pe.WorkingDir, pe.Executable)
}

// ---------------------------------------------------------------------------
// Internal shared helpers
// ---------------------------------------------------------------------------

// extractBaseName returns the base name of a path without extension.
func extractBaseName(path string) string {
	// Handle both Unix and Windows separators.
	base := path
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		base = path[idx+1:]
	}
	// Remove common extensions.
	base = strings.TrimSuffix(base, ".exe")
	return base
}
