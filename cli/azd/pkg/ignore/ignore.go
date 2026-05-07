// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ignore

import (
	"bytes"
	"errors"
	"fmt"
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
// It loads both .azdxignore and .gitignore. Patterns are evaluated additively — a path is
// ignored if it matches ANY pattern in EITHER file. This means .gitignore negation patterns (!)
// cannot un-ignore paths that are matched by .azdxignore, since each file is parsed independently.
// Missing files are silently skipped (no error). A non-nil Matcher is always returned
// even when no ignore files exist (it simply matches nothing).
func NewMatcher(root string) (*Matcher, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root path: %w", err)
	}

	m := &Matcher{root: absRoot}

	// Load .azdxignore first — any match from either file causes the path to be
	// ignored (union semantics). Negation patterns in one file do not override
	// matches in the other, because each file is parsed independently.
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
