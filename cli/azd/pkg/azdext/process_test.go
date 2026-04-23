// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// IsProcessRunning
// ---------------------------------------------------------------------------

func TestIsProcessRunning_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	if !IsProcessRunning(pid) {
		t.Errorf("IsProcessRunning(%d) = false for current process, want true", pid)
	}
}

func TestIsProcessRunning_InvalidPID(t *testing.T) {
	if IsProcessRunning(0) {
		t.Error("IsProcessRunning(0) = true, want false")
	}
	if IsProcessRunning(-1) {
		t.Error("IsProcessRunning(-1) = true, want false")
	}
}

func TestIsProcessRunning_NonexistentPID(t *testing.T) {
	// PID 99999999 is extremely unlikely to exist.
	if IsProcessRunning(99999999) {
		t.Error("IsProcessRunning(99999999) = true, want false")
	}
}

// ---------------------------------------------------------------------------
// GetProcessInfo
// ---------------------------------------------------------------------------

func TestGetProcessInfo_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	info := GetProcessInfo(pid)
	if !info.Running {
		t.Errorf("GetProcessInfo(%d).Running = false for current process, want true", pid)
	}
	if info.PID != pid {
		t.Errorf("GetProcessInfo(%d).PID = %d, want %d", pid, info.PID, pid)
	}
	// Name should be non-empty for the current process.
	if info.Name == "" {
		t.Errorf("GetProcessInfo(%d).Name is empty, want non-empty", pid)
	}
}

func TestGetProcessInfo_InvalidPID(t *testing.T) {
	info := GetProcessInfo(-1)
	if info.Running {
		t.Error("GetProcessInfo(-1).Running = true, want false")
	}
}

func TestGetProcessInfo_NonexistentPID(t *testing.T) {
	info := GetProcessInfo(99999999)
	if info.Running {
		t.Error("GetProcessInfo(99999999).Running = true, want false")
	}
}

// ---------------------------------------------------------------------------
// CurrentProcessInfo
// ---------------------------------------------------------------------------

func TestCurrentProcessInfo(t *testing.T) {
	info := CurrentProcessInfo()
	if !info.Running {
		t.Error("CurrentProcessInfo().Running = false, want true")
	}
	if info.PID != os.Getpid() {
		t.Errorf("CurrentProcessInfo().PID = %d, want %d", info.PID, os.Getpid())
	}
	if info.Executable == "" {
		t.Error("CurrentProcessInfo().Executable is empty")
	}
	if info.Name == "" {
		t.Error("CurrentProcessInfo().Name is empty")
	}
}

// ---------------------------------------------------------------------------
// ParentProcessInfo
// ---------------------------------------------------------------------------

func TestParentProcessInfo(t *testing.T) {
	info := ParentProcessInfo()
	// Parent process should exist (the test runner).
	if info.PID <= 0 {
		t.Errorf("ParentProcessInfo().PID = %d, want > 0", info.PID)
	}
	// In most environments, the parent should be running.
	// Skip assertion on Running since it depends on the test environment.
}

// ---------------------------------------------------------------------------
// FindProcessByName
// ---------------------------------------------------------------------------

func TestFindProcessByName_Empty(t *testing.T) {
	results := FindProcessByName("")
	if results == nil {
		t.Error("FindProcessByName(\"\") returned nil, want empty slice")
	}
	if len(results) != 0 {
		t.Errorf("FindProcessByName(\"\") returned %d results, want 0", len(results))
	}
}

func TestFindProcessByName_CurrentProcess(t *testing.T) {
	// Get current process name.
	current := CurrentProcessInfo()
	if current.Name == "" {
		t.Skip("cannot determine current process name")
	}

	results := FindProcessByName(current.Name)
	if len(results) == 0 {
		t.Errorf("FindProcessByName(%q) returned 0 results, want >= 1", current.Name)
	}

	// Verify at least one result matches our PID.
	found := false
	for _, r := range results {
		if r.PID == current.PID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FindProcessByName(%q) did not find current process PID %d", current.Name, current.PID)
	}
}

func TestFindProcessByName_Nonexistent(t *testing.T) {
	results := FindProcessByName("azdext-nonexistent-process-xyz")
	if results == nil {
		t.Error("FindProcessByName(nonexistent) returned nil, want empty slice")
	}
	if len(results) != 0 {
		t.Errorf("FindProcessByName(nonexistent) returned %d results, want 0", len(results))
	}
}

// ---------------------------------------------------------------------------
// ProcessEnvironment
// ---------------------------------------------------------------------------

func TestGetProcessEnvironment(t *testing.T) {
	env := GetProcessEnvironment()

	if env.PID != os.Getpid() {
		t.Errorf("ProcessEnvironment.PID = %d, want %d", env.PID, os.Getpid())
	}
	if env.PPID <= 0 {
		t.Errorf("ProcessEnvironment.PPID = %d, want > 0", env.PPID)
	}
	if env.OS != runtime.GOOS {
		t.Errorf("ProcessEnvironment.OS = %q, want %q", env.OS, runtime.GOOS)
	}
	if env.Arch != runtime.GOARCH {
		t.Errorf("ProcessEnvironment.Arch = %q, want %q", env.Arch, runtime.GOARCH)
	}
	if env.NumCPU <= 0 {
		t.Errorf("ProcessEnvironment.NumCPU = %d, want > 0", env.NumCPU)
	}
	if env.Executable == "" {
		t.Error("ProcessEnvironment.Executable is empty")
	}
}

func TestProcessEnvironment_String(t *testing.T) {
	env := GetProcessEnvironment()
	s := env.String()

	if !strings.Contains(s, "pid=") {
		t.Errorf("ProcessEnvironment.String() = %q, missing pid=", s)
	}
	if !strings.Contains(s, "os=") {
		t.Errorf("ProcessEnvironment.String() = %q, missing os=", s)
	}
	if !strings.Contains(s, "arch=") {
		t.Errorf("ProcessEnvironment.String() = %q, missing arch=", s)
	}
}

// ---------------------------------------------------------------------------
// extractBaseName
// ---------------------------------------------------------------------------

func TestExtractBaseName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/usr/bin/bash", "bash"},
		{`C:\Windows\System32\cmd.exe`, "cmd"},
		{"simple", "simple"},
		{"program.exe", "program"},
		{"/path/to/my-tool", "my-tool"},
		{`C:\tools\mytool.exe`, "mytool"},
		{"", ""},
	}
	for _, tc := range tests {
		got := extractBaseName(tc.input)
		if got != tc.want {
			t.Errorf("extractBaseName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
