// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ---------------------------------------------------------------------------
// Tool discovery and PATH management
// ---------------------------------------------------------------------------

// ToolInfo contains information about a discovered tool on PATH.
type ToolInfo struct {
	// Name is the tool name as requested (e.g., "docker").
	Name string
	// Path is the absolute filesystem path to the resolved executable.
	Path string
	// Found is true if the tool was located on PATH.
	Found bool
}

// LookupTool searches for the named executable on the system PATH.
// If not found on PATH, it also checks the current working directory for a
// project-local executable with the same name (for example, ./mvnw).
//
// Platform behavior:
//   - Windows: Searches PATH and PATHEXT extensions (.exe, .cmd, .bat, etc.).
//   - Unix: Searches PATH for files with the executable bit set.
//
// LookupTool never returns an error; if the tool is not found, Found is false
// and Path is empty.
func LookupTool(name string) ToolInfo {
	if p, ok := lookupProjectLocalTool(name); ok {
		return ToolInfo{Name: name, Path: p, Found: true}
	}

	p, err := exec.LookPath(name)
	if err != nil {
		return ToolInfo{Name: name, Found: false}
	}

	// Resolve to absolute path for consistent results.
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}

	return ToolInfo{Name: name, Path: abs, Found: true}
}

// LookupTools searches for multiple tools on PATH in a single call.
// Returns a map of tool name → [ToolInfo]. All tools are looked up
// regardless of whether earlier ones are found.
func LookupTools(names ...string) map[string]ToolInfo {
	result := make(map[string]ToolInfo, len(names))
	for _, name := range names {
		result[name] = LookupTool(name)
	}
	return result
}

// RequireTools checks that all named tools are available on PATH.
// Returns nil if all tools are found, or a [*ToolsNotFoundError] listing
// the missing tools.
//
// This is useful in extension preRun hooks to fail fast with a clear message:
//
//	if err := azdext.RequireTools("docker", "kubectl"); err != nil {
//	    return err
//	}
func RequireTools(names ...string) error {
	var missing []string
	for _, name := range names {
		info := LookupTool(name)
		if !info.Found {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return &ToolsNotFoundError{Tools: missing}
	}
	return nil
}

// ToolsNotFoundError reports one or more required tools that are not on PATH.
type ToolsNotFoundError struct {
	// Tools lists the names of missing tools.
	Tools []string
}

func (e *ToolsNotFoundError) Error() string {
	return fmt.Sprintf("azdext.RequireTools: required tools not found on PATH: %s",
		strings.Join(e.Tools, ", "))
}

// ---------------------------------------------------------------------------
// PATH management
// ---------------------------------------------------------------------------

// PrependPATH adds dirs to the front of the PATH environment variable
// and sets it in the current process environment. Duplicate entries already
// in PATH are not added again.
//
// Platform behavior:
//   - Windows: Uses ';' as the path separator.
//   - Unix: Uses ':' as the path separator.
//
// PrependPATH modifies the current process environment. It does not affect
// parent or child processes beyond normal inheritance.
func PrependPATH(dirs ...string) error {
	current := os.Getenv("PATH")
	newPath := buildPATH(dirs, current)
	return os.Setenv("PATH", newPath)
}

// AppendPATH adds dirs to the end of the PATH environment variable.
// Duplicate entries already in PATH are not added again.
//
// Platform behavior: see [PrependPATH].
func AppendPATH(dirs ...string) error {
	current := os.Getenv("PATH")
	existing := splitPATH(current)

	existingSet := make(map[string]bool, len(existing))
	for _, p := range existing {
		existingSet[normalizePATHEntry(p)] = true
	}

	parts := make([]string, 0, len(existing)+len(dirs))
	parts = append(parts, existing...)
	for _, d := range dirs {
		if !existingSet[normalizePATHEntry(d)] {
			parts = append(parts, d)
			existingSet[normalizePATHEntry(d)] = true
		}
	}

	return os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}

// PATHContains reports whether dir is present in the current PATH.
//
// Platform behavior:
//   - Windows: Comparison is case-insensitive and normalizes path separators.
//   - Unix: Comparison is case-sensitive and exact.
func PATHContains(dir string) bool {
	entries := splitPATH(os.Getenv("PATH"))
	target := normalizePATHEntry(dir)
	for _, entry := range entries {
		if normalizePATHEntry(entry) == target {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// buildPATH constructs a new PATH with dirs prepended, deduplicating.
func buildPATH(dirs []string, current string) string {
	existing := splitPATH(current)

	parts := make([]string, 0, len(dirs)+len(existing))
	seen := make(map[string]bool, len(dirs)+len(existing))

	// Add new dirs first (prepend).
	for _, d := range dirs {
		norm := normalizePATHEntry(d)
		if !seen[norm] {
			parts = append(parts, d)
			seen[norm] = true
		}
	}

	// Add existing PATH entries that aren't duplicates of new dirs.
	for _, p := range existing {
		norm := normalizePATHEntry(p)
		if !seen[norm] {
			parts = append(parts, p)
			seen[norm] = true
		}
	}

	return strings.Join(parts, string(os.PathListSeparator))
}

// splitPATH splits a PATH string into individual entries.
func splitPATH(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, string(os.PathListSeparator))
}

// normalizePATHEntry normalizes a PATH entry for comparison.
// On Windows, this lowercases and normalizes path separators.
// On Unix, this is a no-op (case-sensitive).
func normalizePATHEntry(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(filepath.Clean(p))
	}
	return filepath.Clean(p)
}

func lookupProjectLocalTool(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", false
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}

	base := filepath.Join(cwd, name)
	for _, candidate := range executableCandidates(base) {
		if isExecutableFile(candidate) {
			return candidate, true
		}
	}

	return "", false
}

func executableCandidates(base string) []string {
	if runtime.GOOS != "windows" {
		return []string{base}
	}

	if ext := strings.ToLower(filepath.Ext(base)); ext != "" {
		return []string{base}
	}

	exts := strings.Split(os.Getenv("PATHEXT"), ";")
	if len(exts) == 0 || (len(exts) == 1 && exts[0] == "") {
		exts = []string{".exe", ".cmd", ".bat", ".com"}
	}

	candidates := make([]string, 0, len(exts)+1)
	candidates = append(candidates, base)
	for _, ext := range exts {
		if ext == "" {
			continue
		}
		candidates = append(candidates, base+strings.ToLower(ext))
		candidates = append(candidates, base+strings.ToUpper(ext))
	}
	return candidates
}

func isExecutableFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return fi.Mode()&0o111 != 0
}
