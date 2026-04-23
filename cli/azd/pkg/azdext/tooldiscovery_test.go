// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// LookupTool
// ---------------------------------------------------------------------------

func TestLookupTool_KnownTool(t *testing.T) {
	// "go" should be on PATH in any environment running Go tests.
	info := LookupTool("go")
	if !info.Found {
		t.Fatal("LookupTool(\"go\") not found, expected on PATH")
	}
	if info.Name != "go" {
		t.Errorf("LookupTool(\"go\").Name = %q, want %q", info.Name, "go")
	}
	if info.Path == "" {
		t.Error("LookupTool(\"go\").Path is empty")
	}
}

func TestLookupTool_NotFound(t *testing.T) {
	info := LookupTool("azdext-nonexistent-tool-abc123")
	if info.Found {
		t.Error("LookupTool(nonexistent) found, want not found")
	}
	if info.Name != "azdext-nonexistent-tool-abc123" {
		t.Errorf("LookupTool(nonexistent).Name = %q, want original name", info.Name)
	}
	if info.Path != "" {
		t.Errorf("LookupTool(nonexistent).Path = %q, want empty", info.Path)
	}
}

func TestLookupTool_ProjectLocalTool(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir() error: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	toolName := "azdext-local-tool"
	toolPath := toolName
	toolContent := "#!/bin/sh\necho ok\n"
	mode := os.FileMode(0o755)
	if runtime.GOOS == "windows" {
		toolPath = toolName + ".cmd"
		toolContent = "@echo off\r\necho ok\r\n"
		mode = 0o644
	}
	if err := os.WriteFile(toolPath, []byte(toolContent), mode); err != nil {
		t.Fatalf("WriteFile(%q) error: %v", toolPath, err)
	}

	info := LookupTool(toolName)
	if !info.Found {
		t.Fatalf("LookupTool(%q).Found = false, want true", toolName)
	}
	if info.Path == "" {
		t.Fatalf("LookupTool(%q).Path is empty", toolName)
	}
	if !strings.Contains(strings.ToLower(info.Path), strings.ToLower(toolName)) {
		t.Fatalf("LookupTool(%q).Path = %q, want project-local path", toolName, info.Path)
	}
}

// ---------------------------------------------------------------------------
// LookupTools
// ---------------------------------------------------------------------------

func TestLookupTools_Mixed(t *testing.T) {
	results := LookupTools("go", "azdext-nonexistent-tool-xyz789")

	goInfo, ok := results["go"]
	if !ok {
		t.Fatal("LookupTools() missing 'go' key")
	}
	if !goInfo.Found {
		t.Error("LookupTools(): go not found")
	}

	noInfo, ok := results["azdext-nonexistent-tool-xyz789"]
	if !ok {
		t.Fatal("LookupTools() missing nonexistent key")
	}
	if noInfo.Found {
		t.Error("LookupTools(): nonexistent tool reported as found")
	}
}

// ---------------------------------------------------------------------------
// RequireTools
// ---------------------------------------------------------------------------

func TestRequireTools_AllPresent(t *testing.T) {
	if err := RequireTools("go"); err != nil {
		t.Errorf("RequireTools(\"go\") = %v, want nil", err)
	}
}

func TestRequireTools_SomeMissing(t *testing.T) {
	err := RequireTools("go", "azdext-tool-missing-1", "azdext-tool-missing-2")
	if err == nil {
		t.Fatal("RequireTools() with missing tools = nil, want error")
	}

	var tnf *ToolsNotFoundError
	if ok := isToolsNotFoundError(err, &tnf); !ok {
		t.Fatalf("RequireTools() returned %T, want *ToolsNotFoundError", err)
	}

	if len(tnf.Tools) != 2 {
		t.Errorf("ToolsNotFoundError.Tools length = %d, want 2", len(tnf.Tools))
	}

	msg := tnf.Error()
	if !strings.Contains(msg, "azdext-tool-missing-1") || !strings.Contains(msg, "azdext-tool-missing-2") {
		t.Errorf("error message %q missing tool names", msg)
	}
}

func TestRequireTools_Empty(t *testing.T) {
	if err := RequireTools(); err != nil {
		t.Errorf("RequireTools() with no args = %v, want nil", err)
	}
}

// ---------------------------------------------------------------------------
// PATH management
// ---------------------------------------------------------------------------

func TestPrependPATH(t *testing.T) {
	original := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", original) })

	newDir := t.TempDir()
	if err := PrependPATH(newDir); err != nil {
		t.Fatalf("PrependPATH() error: %v", err)
	}

	current := os.Getenv("PATH")
	if !strings.HasPrefix(current, newDir) {
		t.Errorf("PATH should start with %q, got %q", newDir, current[:min(len(newDir)+10, len(current))])
	}
}

func TestPrependPATH_Deduplicates(t *testing.T) {
	original := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", original) })

	newDir := t.TempDir()
	if err := PrependPATH(newDir); err != nil {
		t.Fatalf("PrependPATH(1) error: %v", err)
	}
	if err := PrependPATH(newDir); err != nil {
		t.Fatalf("PrependPATH(2) error: %v", err)
	}

	// Count occurrences.
	entries := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	count := 0
	target := normalizePATHEntry(newDir)
	for _, e := range entries {
		if normalizePATHEntry(e) == target {
			count++
		}
	}
	if count != 1 {
		t.Errorf("PrependPATH() duplicated entry: found %d occurrences", count)
	}
}

func TestAppendPATH(t *testing.T) {
	original := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", original) })

	newDir := t.TempDir()
	if err := AppendPATH(newDir); err != nil {
		t.Fatalf("AppendPATH() error: %v", err)
	}

	current := os.Getenv("PATH")
	if !strings.HasSuffix(current, newDir) {
		end := current
		if len(end) > 100 {
			end = "..." + end[len(end)-100:]
		}
		t.Errorf("PATH should end with %q, got %q", newDir, end)
	}
}

func TestPATHContains(t *testing.T) {
	original := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", original) })

	newDir := t.TempDir()

	if PATHContains(newDir) {
		t.Error("PATHContains() before adding = true, want false")
	}

	if err := PrependPATH(newDir); err != nil {
		t.Fatalf("PrependPATH() error: %v", err)
	}

	if !PATHContains(newDir) {
		t.Error("PATHContains() after adding = false, want true")
	}
}

func TestPATHContains_CaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive PATH is Windows-specific")
	}

	original := os.Getenv("PATH")
	t.Cleanup(func() { os.Setenv("PATH", original) })

	dir := `C:\Some\Test\Path`
	os.Setenv("PATH", dir+string(os.PathListSeparator)+original)

	if !PATHContains(`c:\some\test\path`) {
		t.Error("PATHContains() case-insensitive on Windows = false, want true")
	}
}

// ---------------------------------------------------------------------------
// normalizePATHEntry
// ---------------------------------------------------------------------------

func TestNormalizePATHEntry(t *testing.T) {
	tests := []struct {
		input string
		goos  string
	}{
		{"/usr/bin", "linux"},
		{`C:\Windows`, "windows"},
		{"/usr/local/bin/", "linux"}, // trailing slash cleaned
	}
	for _, tc := range tests {
		got := normalizePATHEntry(tc.input)
		if got == "" {
			t.Errorf("normalizePATHEntry(%q) returned empty", tc.input)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isToolsNotFoundError(err error, target **ToolsNotFoundError) bool {
	return errors.As(err, target)
}
