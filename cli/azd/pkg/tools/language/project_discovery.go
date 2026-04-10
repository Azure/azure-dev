// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package language provides project file discovery and dependency
// management for multi-language hook scripts.
package language

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ProjectContext holds metadata about a discovered project file,
// used to determine how to install dependencies for a hook script.
type ProjectContext struct {
	// ProjectDir is the directory containing the project file.
	ProjectDir string
	// DependencyFile is the absolute path to the dependency file
	// (e.g. requirements.txt, package.json, *.csproj).
	DependencyFile string
	// Language is the hook kind inferred from the project file.
	Language HookKind
}

// projectFileEntry maps a filename or glob pattern to a language.
type projectFileEntry struct {
	Name     string   // exact filename or glob pattern
	Language HookKind // inferred hook kind
	IsGlob   bool     // true for patterns like "*.*proj"
}

// knownProjectFiles defines project files to search for, in priority order.
// The first match found in a directory wins.
//
// Python: pyproject.toml is preferred over requirements.txt, matching the
// convention in framework_service_python.go and internal/appdetect/python.go
// (PEP 621 preference).
//
// NOTE: This is intentionally separate from internal/appdetect/ which walks
// DOWN a tree to detect service projects. Hook discovery walks UP from a
// script to find the nearest project context.
var knownProjectFiles = []projectFileEntry{
	{Name: "pyproject.toml", Language: HookKindPython},
	{Name: "requirements.txt", Language: HookKindPython},
	{Name: "package.json", Language: HookKindJavaScript},
	{Name: "*.*proj", Language: HookKindDotNet, IsGlob: true},
}

// DiscoverProjectFile walks up the directory tree from the directory
// containing scriptPath, looking for known project files to infer the
// project context for dependency installation.
//
// The search stops at boundaryDir to prevent path traversal outside
// the project or service root. Returns nil without error when no
// project file is found — hooks can still run without project context.
func DiscoverProjectFile(
	scriptPath string, boundaryDir string,
) (*ProjectContext, error) {
	scriptDir := filepath.Dir(scriptPath)

	absScript, err := filepath.Abs(scriptDir)
	if err != nil {
		return nil, fmt.Errorf(
			"resolving script directory %q: %w", scriptDir, err,
		)
	}

	absBoundary, err := filepath.Abs(boundaryDir)
	if err != nil {
		return nil, fmt.Errorf(
			"resolving boundary directory %q: %w",
			boundaryDir, err,
		)
	}

	current := absScript
	for {
		result, err := discoverInDirectory(current)
		if err != nil {
			return nil, err
		}
		if result != nil {
			return result, nil
		}

		// Stop when we've reached the boundary directory.
		if pathsEqual(current, absBoundary) {
			return nil, nil
		}

		parent := filepath.Dir(current)
		// Stop at filesystem root (parent == current).
		if parent == current {
			return nil, nil
		}
		current = parent
	}
}

// discoverInDirectory scans a single directory for known project
// files. Returns the first match or nil if none is found.
func discoverInDirectory(dir string) (*ProjectContext, error) {
	for _, entry := range knownProjectFiles {
		if entry.IsGlob {
			pattern := filepath.Join(dir, entry.Name)
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return nil, fmt.Errorf(
					"glob %q in %q: %w",
					entry.Name, dir, err,
				)
			}
			if len(matches) > 0 {
				return &ProjectContext{
					ProjectDir:     dir,
					DependencyFile: matches[0],
					Language:       entry.Language,
				}, nil
			}
		} else {
			candidate := filepath.Join(dir, entry.Name)
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() {
				return &ProjectContext{
					ProjectDir:     dir,
					DependencyFile: candidate,
					Language:       entry.Language,
				}, nil
			}
		}
	}
	return nil, nil
}

// DiscoverNodeProject walks up the directory tree from the directory
// containing scriptPath, looking specifically for package.json.
//
// Unlike [DiscoverProjectFile] which searches for all known project
// files in priority order, this function only matches package.json.
// This avoids false negatives in mixed-language directories where a
// Python project file (higher priority in the generic list) would
// shadow the Node.js project file.
//
// The search stops at boundaryDir. Returns nil without error when
// no package.json is found.
func DiscoverNodeProject(
	scriptPath string, boundaryDir string,
) (*ProjectContext, error) {
	scriptDir := filepath.Dir(scriptPath)

	absScript, err := filepath.Abs(scriptDir)
	if err != nil {
		return nil, fmt.Errorf(
			"resolving script directory %q: %w", scriptDir, err,
		)
	}

	absBoundary, err := filepath.Abs(boundaryDir)
	if err != nil {
		return nil, fmt.Errorf(
			"resolving boundary directory %q: %w",
			boundaryDir, err,
		)
	}

	current := absScript
	for {
		candidate := filepath.Join(current, "package.json")
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return &ProjectContext{
				ProjectDir:     current,
				DependencyFile: candidate,
				Language:       HookKindJavaScript,
			}, nil
		}

		// Stop when we've reached the boundary directory.
		if pathsEqual(current, absBoundary) {
			return nil, nil
		}

		parent := filepath.Dir(current)
		// Stop at filesystem root (parent == current).
		if parent == current {
			return nil, nil
		}
		current = parent
	}
}

// pathsEqual compares two cleaned absolute paths for equality.
// On Windows the comparison is case-insensitive to match the
// filesystem behavior.
func pathsEqual(a, b string) bool {
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(cleanA, cleanB)
	}
	return cleanA == cleanB
}
