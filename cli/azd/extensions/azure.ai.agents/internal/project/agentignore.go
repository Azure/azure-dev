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
	"path"
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

// utf8BOM is the byte order mark that some Windows editors prepend to UTF-8 files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// agentIgnoreMatcher provides path matching for agent code deploy packaging.
type agentIgnoreMatcher struct {
	ignore        gitignore.GitIgnore // from .agentignore file or defaults
	hasUserIgnore bool

	// content and base are what ignore was built from, kept so that
	// keepPythonBytecode can build a second matcher from the same rules.
	content []byte
	base    string

	// keepBytecode is set by keepPythonBytecode. It holds the same rules as ignore
	// minus the generic __pycache__/ and *.pyc exclusions.
	keepBytecode gitignore.GitIgnore
}

// newAgentIgnoreMatcher creates a matcher by reading .agentignore from srcDir.
// If no .agentignore exists, defaults are used.
func newAgentIgnoreMatcher(ctx context.Context, srcDir string) (*agentIgnoreMatcher, error) {
	_ = ctx // reserved for future cancellation support
	m := &agentIgnoreMatcher{base: srcDir}

	// Try to load user's .agentignore
	data, err := loadAgentIgnore(ctx, srcDir)
	if err != nil {
		return nil, err
	}

	if data != nil {
		m.content = data
		m.hasUserIgnore = true
	} else {
		// No .agentignore file — use defaults
		m.content = []byte(defaultExclusionsContent)
	}
	m.ignore = gitignore.New(bytes.NewReader(m.content), srcDir, nil)

	return m, nil
}

// keepPythonBytecode makes the matcher keep Python bytecode that .agentignore (or the
// defaults) would exclude only through the generic __pycache__/ and *.pyc rules.
// Python bundled mode uses it: the hosted runtime cannot write bytecode into the
// read-only package, so stripping prebuilt .pyc files makes every cold start
// recompile each imported module.
//
// Every other rule still applies. A .pyc file is still excluded when another rule
// matches it, or when its source .py file is excluded, because the bytecode holds
// the same literals as the source. Files in __pycache__/ that are not .pyc files
// are still excluded.
func (m *agentIgnoreMatcher) keepPythonBytecode() {
	m.keepBytecode = gitignore.New(
		strings.NewReader(withoutBytecodeRules(string(m.content))), m.base, nil)
}

// withoutBytecodeRules drops the generic Python bytecode patterns from gitignore
// content. Rules that name a specific path (e.g. secrets/__pycache__/) are kept.
func withoutBytecodeRules(content string) string {
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case "__pycache__", "__pycache__/", "**/__pycache__", "**/__pycache__/",
			"*.pyc", "**/*.pyc", "*$py.class":
			continue
		case "*.py[cod]":
			line = "*.py[od]"
		case "*.py[co]":
			line = "*.pyo"
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// ShouldExclude returns true if the given path should be excluded from the ZIP.
// relPath is the path relative to srcDir using forward slashes.
// isDir indicates whether the path is a directory.
func (m *agentIgnoreMatcher) ShouldExclude(relPath string, isDir bool) bool {
	if m.keepBytecode != nil {
		return m.shouldExcludeKeepingBytecode(relPath, isDir)
	}
	return ignored(m.ignore, relPath, isDir)
}

func (m *agentIgnoreMatcher) shouldExcludeKeepingBytecode(relPath string, isDir bool) bool {
	if ignored(m.keepBytecode, relPath, isDir) {
		return true
	}
	if isDir {
		return false
	}

	dir, name := path.Split(relPath)
	dir = strings.TrimSuffix(dir, "/")
	inCache := path.Base(dir) == "__pycache__"

	if !strings.HasSuffix(name, ".pyc") {
		// The walker only enters __pycache__/ because of the bytecode exception, so
		// anything else in it keeps the exclusion the full rules give the directory.
		return inCache && ignored(m.ignore, dir, true)
	}

	// Check the source module against the rules too: pkg/__pycache__/mod.cpython-313.pyc
	// comes from pkg/mod.py, and a legacy pkg/mod.pyc from pkg/mod.py.
	source := strings.TrimSuffix(relPath, "c")
	if inCache {
		module, _, _ := strings.Cut(name, ".")
		source = path.Join(path.Dir(dir), module+".py")
	}
	return ignored(m.keepBytecode, source, false)
}

func ignored(ig gitignore.GitIgnore, relPath string, isDir bool) bool {
	match := ig.Relative(relPath, isDir)
	return match != nil && match.Ignore()
}

// loadAgentIgnore reads an .agentignore file from srcDir and returns its rules, with
// the azd-managed exclusions prepended. Returns nil, nil if no file exists.
func loadAgentIgnore(ctx context.Context, srcDir string) ([]byte, error) {
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
	return append([]byte(azdManagedExclusionsContent+"\n"), data...), nil
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

# Security / secrets
.env
.env.*
.azure/
.git/

# Python
# Bundled deploys (dependency_resolution: bundled) keep __pycache__/ and *.pyc despite
# the two rules below, so prebuilt bytecode speeds up cold starts. Other rules still
# apply to bytecode. To ship no bytecode, delete __pycache__/ directories before deploying.
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
