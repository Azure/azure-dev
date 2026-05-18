// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	gitignore "github.com/denormal/go-gitignore"
)

const (
	agentIgnoreFileName = ".agentignore"
	agentIgnoreMaxSize  = 1 << 20 // 1 MB
)

// securityExclusions are directory paths that are always excluded regardless of .agentignore content.
// Users cannot negate these with "!" patterns.
// Note: .env/.env.* files are handled separately in isSecurityExcluded.
var securityExclusions = []string{
	".azure/",
	".git/",
}

// metadataExclusions are files that are always excluded because they are deployment metadata
// sent separately or not relevant inside the code package.
var metadataExclusions = []string{
	"agent.yaml",
	"agent.manifest.yaml",
	"azure.yaml",
	agentIgnoreFileName,
}

// defaultExclusions are applied when no .agentignore file exists.
// Generated from DefaultAgentIgnoreContent() to maintain a single source of truth.
var defaultExclusionsContent = DefaultAgentIgnoreContent()

// utf8BOM is the byte order mark that some Windows editors prepend to UTF-8 files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// agentIgnoreMatcher provides path matching for agent code deploy packaging.
type agentIgnoreMatcher struct {
	userIgnore    gitignore.GitIgnore // from .agentignore file (nil if no file)
	securityPaths []string            // always excluded, non-negotiable
	defaultIgnore gitignore.GitIgnore // used when no .agentignore file exists
	hasUserIgnore bool
}

// newAgentIgnoreMatcher creates a matcher by reading .agentignore from srcDir.
// If no .agentignore exists, defaults are used.
func newAgentIgnoreMatcher(srcDir string) (*agentIgnoreMatcher, error) {
	m := &agentIgnoreMatcher{
		securityPaths: securityExclusions,
	}

	// Try to load user's .agentignore
	ig, err := loadAgentIgnore(srcDir)
	if err != nil {
		return nil, err
	}

	if ig != nil {
		m.userIgnore = ig
		m.hasUserIgnore = true
	} else {
		// No .agentignore file — use defaults
		m.defaultIgnore = gitignore.New(
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
	// Security exclusions always apply — cannot be overridden
	if m.isSecurityExcluded(relPath, isDir) {
		return true
	}

	if m.hasUserIgnore {
		match := m.userIgnore.Relative(relPath, isDir)
		if match != nil && match.Ignore() {
			return true
		}
		return false
	}

	// No .agentignore: use built-in defaults
	match := m.defaultIgnore.Relative(relPath, isDir)
	if match != nil && match.Ignore() {
		return true
	}
	return false
}

// isSecurityExcluded checks if a path matches the non-negotiable security or metadata exclusions.
func (m *agentIgnoreMatcher) isSecurityExcluded(relPath string, isDir bool) bool {
	name := filepath.Base(relPath)

	// .env and .env.* files
	if !isDir && (name == ".env" || strings.HasPrefix(name, ".env.")) {
		return true
	}

	// Metadata files (agent.yaml, azure.yaml, etc.) — only at the root level
	if !isDir && filepath.Dir(relPath) == "." {
		if slices.Contains(metadataExclusions, name) {
			return true
		}
	}

	// .azure/ and .git/ directories
	for _, sec := range m.securityPaths {
		if before, ok := strings.CutSuffix(sec, "/"); ok {
			// Directory exclusion
			dirName := before
			if isDir && name == dirName {
				return true
			}
			// Also match files inside these dirs (e.g., ".azure/foo")
			if strings.HasPrefix(relPath, dirName+"/") {
				return true
			}
		}
	}

	return false
}

// loadAgentIgnore reads an .agentignore file from srcDir.
// Returns nil, nil if no file exists.
func loadAgentIgnore(srcDir string) (gitignore.GitIgnore, error) {
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
	var sb strings.Builder
	sb.WriteString("# Files excluded from agent code deployment packaging.\n")
	sb.WriteString("# Uses .gitignore syntax. Security files (.env, .azure/, .git/) are always\n")
	sb.WriteString("# excluded regardless of this file.\n")
	sb.WriteString("#\n")
	sb.WriteString("# To include a file that is excluded by default, use negation: !filename\n")
	sb.WriteString("\n")
	sb.WriteString("# azd tooling files\n")
	sb.WriteString("agent.yaml\n")
	sb.WriteString("agent.manifest.yaml\n")
	sb.WriteString("azure.yaml\n")
	sb.WriteString(".agentignore\n")
	sb.WriteString("\n")
	sb.WriteString("# Python\n")
	sb.WriteString("__pycache__/\n")
	sb.WriteString(".venv/\n")
	sb.WriteString("venv/\n")
	sb.WriteString("*.pyc\n")
	sb.WriteString("*.pyo\n")
	sb.WriteString(".mypy_cache/\n")
	sb.WriteString(".pytest_cache/\n")
	sb.WriteString("\n")
	sb.WriteString("# .NET\n")
	sb.WriteString("bin/\n")
	sb.WriteString("obj/\n")
	sb.WriteString("*.user\n")
	sb.WriteString("*.suo\n")
	sb.WriteString(".vs/\n")
	sb.WriteString("\n")
	sb.WriteString("# Node\n")
	sb.WriteString("node_modules/\n")
	sb.WriteString("\n")
	sb.WriteString("# Git\n")
	sb.WriteString(".git/\n")
	return sb.String()
}
