// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// utf8BOM is the byte order mark that some Windows editors prepend to UTF-8 files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// agentIgnoreMatcher provides path matching for agent code deploy packaging.
type agentIgnoreMatcher struct {
	ignore        gitignore.GitIgnore // from .agentignore file or defaults
	hasUserIgnore bool
}

// newAgentIgnoreMatcher creates a matcher by reading .agentignore from srcDir.
// If no .agentignore exists, defaults are used.
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

	return gitignore.New(bytes.NewReader(data), srcDir, nil), nil
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
.agentignore

# Security / secrets
.env
.env.*
.azure/
.git/

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
