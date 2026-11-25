// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	gitignore "github.com/denormal/go-gitignore"
)

const (
	agentIgnoreFileName = ".agentignore"
	agentIgnoreMaxSize  = 1 << 20 // 1 MB
)

// defaultExclusionsContent is used as the matcher when no .agentignore file exists.
// Generated from DefaultAgentIgnoreContent() to maintain a single source of truth.
var defaultExclusionsContent = DefaultAgentIgnoreContent()

const azdManagedExclusionsContent = `# azd-managed resource definitions
connection.yaml
connection.yml
toolbox.yaml
toolbox.yml

# azd-generated Teams app artifacts
appPackage.zip
.appPackage.zip.azd-generated
TEAMS_APP_SETUP.md
`

// sensitiveExclusionsContent lists paths that commonly hold secrets or local state:
// .env files, azd environment values under .azure/, and git history. Unlike the other
// defaults, they also apply when the project has its own .agentignore, so a file written
// for an unrelated reason can't silently ship them. Because later gitignore patterns win,
// that file can still re-include one with an explicit negation.
const sensitiveExclusionsContent = `# Security / secrets. Excluded even when removed from this file;
# re-include one only if the agent must deploy it, for example: !.env
.env
.env.*
.azure/
.git/
`

// sensitiveMatcher matches only sensitiveExclusionsContent. Relative matching ignores
// the base directory, so one matcher serves every source directory.
var sensitiveMatcher = gitignore.New(strings.NewReader(sensitiveExclusionsContent), "", nil)

// utf8BOM is the byte order mark that some Windows editors prepend to UTF-8 files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// agentIgnoreMatcher provides path matching for agent code deploy packaging.
type agentIgnoreMatcher struct {
	ignore        gitignore.GitIgnore // from .agentignore file or defaults
	hasUserIgnore bool
}

// newAgentIgnoreMatcher creates a matcher by reading .agentignore from srcDir.
// If no .agentignore exists, defaults are used. A user file replaces the defaults
// except for the sensitive and azd-managed exclusions, which still apply unless the
// file re-includes a path with a negation.
func newAgentIgnoreMatcher(ctx context.Context, srcDir string) (*agentIgnoreMatcher, error) {
	_ = ctx // reserved for future cancellation support
	m := &agentIgnoreMatcher{}

	// Try to load user's .agentignore
	ig, err := loadAgentIgnore(ctx, srcDir)
	if err != nil {
		return nil, err
	}

	if ig != nil {
		m.ignore = ig
		m.hasUserIgnore = true
	} else {
		// No .agentignore file — use defaults
		m.ignore = gitignore.New(
			strings.NewReader(defaultExclusionsContent),
			srcDir,
			nil,
		)
	}

	return m, nil
}

// ShouldExclude returns true if the given path should be excluded from the ZIP.
// relPath is the path relative to srcDir using forward slashes.
// isDir indicates whether the path is a directory.
func (m *agentIgnoreMatcher) ShouldExclude(relPath string, isDir bool) bool {
	match := m.ignore.Relative(relPath, isDir)
	if match != nil && match.Ignore() {
		return true
	}
	return false
}

// loadAgentIgnore reads an .agentignore file from srcDir.
// Returns nil, nil if no file exists.
func loadAgentIgnore(ctx context.Context, srcDir string) (gitignore.GitIgnore, error) {
	_ = ctx // reserved for future cancellation support
	path := filepath.Join(srcDir, agentIgnoreFileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", agentIgnoreFileName, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", agentIgnoreFileName)
	}
	if info.Size() > agentIgnoreMaxSize {
		return nil, fmt.Errorf("%s exceeds maximum size (%d bytes)", agentIgnoreFileName, agentIgnoreMaxSize)
	}

	f, err := os.Open(path) //nolint:gosec // path is constructed from a known directory + constant filename
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", agentIgnoreFileName, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, agentIgnoreMaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", agentIgnoreFileName, err)
	}
	if int64(len(data)) > agentIgnoreMaxSize {
		return nil, fmt.Errorf("%s exceeds maximum size (%d bytes)", agentIgnoreFileName, agentIgnoreMaxSize)
	}

	// Strip UTF-8 BOM
	data = bytes.TrimPrefix(data, utf8BOM)

	// Always-applied exclusions go first. gitignore lets later patterns win, so they hold
	// unless the user's file explicitly re-includes a path with a negation.
	data = append([]byte(sensitiveExclusionsContent+"\n"+azdManagedExclusionsContent+"\n"), data...)

	return gitignore.New(bytes.NewReader(data), srcDir, nil), nil
}

// walkCodePackageFiles calls visit for every file that code deploy packages from srcDir:
// regular files the matcher doesn't exclude, outside excluded directories. relPath is
// relative to srcDir and uses forward slashes.
func walkCodePackageFiles(
	srcDir string,
	matcher *agentIgnoreMatcher,
	visit func(path, relPath string) error,
) error {
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Skip root
		if relPath == "." {
			return nil
		}

		// Normalize to forward slashes for ZIP
		relPath = filepath.ToSlash(relPath)

		// Skip symlinked directories to avoid traversing outside the project root
		if d.IsDir() && d.Type()&fs.ModeSymlink != 0 {
			return filepath.SkipDir
		}

		// Check directory exclusions
		if d.IsDir() {
			if matcher.ShouldExclude(relPath, true) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks to avoid including files outside the agent directory
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		// Check file exclusions
		if matcher.ShouldExclude(relPath, false) {
			return nil
		}

		return visit(path, relPath)
	})
}

// sensitivePathRoot returns the shortest prefix of relPath, a packaged file path relative
// to the source directory, that sensitiveExclusionsContent matches, or "" when none does.
// A matching directory prefix keeps its trailing slash.
func sensitivePathRoot(relPath string) string {
	for i, r := range relPath {
		if r == '/' && matchesSensitivePath(relPath[:i], true) {
			return relPath[:i+1]
		}
	}
	if matchesSensitivePath(relPath, false) {
		return relPath
	}
	return ""
}

func matchesSensitivePath(relPath string, isDir bool) bool {
	match := sensitiveMatcher.Relative(relPath, isDir)
	return match != nil && match.Ignore()
}

// DefaultAgentIgnoreContent returns the default .agentignore file content
// that should be generated during `azd ai agent init`.
func DefaultAgentIgnoreContent() string {
	return `# Files excluded from agent code deployment packaging.
# Uses .gitignore syntax.
# Note: only the root .agentignore is read; subdirectory files are not supported.
#
# To include a file that is excluded by default, use negation: !filename

# azd tooling files
agent.yaml
agent.manifest.yaml
azure.yaml
connection.yaml
connection.yml
toolbox.yaml
toolbox.yml
.agentignore
appPackage.zip
.appPackage.zip.azd-generated
TEAMS_APP_SETUP.md

` + sensitiveExclusionsContent + `
# Python
__pycache__/
.venv/
venv/
*.pyc
*.pyo
.mypy_cache/
.pytest_cache/

# .NET
bin/
obj/
*.user
*.suo
.vs/

# Node
node_modules/

# Docker (not used in code deploy)
Dockerfile
.dockerignore
`
}
