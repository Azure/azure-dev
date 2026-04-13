// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ignore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	gitignore "github.com/denormal/go-gitignore"
)

const (
	// AzdxIgnoreFile is the name of the azd extension ignore file.
	AzdxIgnoreFile = ".azdxignore"

	// GitIgnoreFile is the name of the standard git ignore file.
	GitIgnoreFile = ".gitignore"
)

// Matcher evaluates whether a path should be ignored based on patterns loaded
// from .azdxignore and .gitignore files found in the root directory.
// A nil Matcher is safe to use and never ignores anything.
type Matcher struct {
	root     string
	matchers []gitignore.GitIgnore
}

// NewMatcher creates a Matcher that loads ignore patterns from the given root directory.
// It attempts to load both .azdxignore and .gitignore — patterns from both files are additive.
// Missing files are silently skipped (no error). A non-nil Matcher is always returned
// even when no ignore files exist (it simply matches nothing).
func NewMatcher(root string) (*Matcher, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	m := &Matcher{root: absRoot}

	// Load .azdxignore first (project-specific rules take precedence in ordering,
	// though any match from either file results in ignore).
	if ig, loadErr := loadIgnoreFile(absRoot, AzdxIgnoreFile); loadErr != nil {
		return nil, loadErr
	} else if ig != nil {
		m.matchers = append(m.matchers, ig)
	}

	// Load .gitignore additively.
	if ig, loadErr := loadIgnoreFile(absRoot, GitIgnoreFile); loadErr != nil {
		return nil, loadErr
	} else if ig != nil {
		m.matchers = append(m.matchers, ig)
	}

	return m, nil
}

// IsIgnored reports whether the given path should be ignored.
// path must be relative to the root directory that was passed to NewMatcher.
// isDir indicates whether the path refers to a directory.
// A nil Matcher always returns false.
func (m *Matcher) IsIgnored(path string, isDir bool) bool {
	if m == nil || len(m.matchers) == 0 {
		return false
	}

	// Normalize to forward slashes — gitignore patterns are slash-based.
	rel := filepath.ToSlash(path)

	for _, ig := range m.matchers {
		if match := ig.Relative(rel, isDir); match != nil && match.Ignore() {
			return true
		}
	}

	return false
}

// utf8BOM is the byte order mark that some Windows editors prepend to UTF-8 files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// loadIgnoreFile reads and parses a single ignore file from root/name.
// Returns (nil, nil) if the file does not exist.
func loadIgnoreFile(root, name string) (gitignore.GitIgnore, error) {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	// Strip UTF-8 BOM if present — matches pattern used in project_utils.go.
	data = bytes.TrimPrefix(data, utf8BOM)

	return gitignore.New(bytes.NewReader(data), root, nil), nil
}
