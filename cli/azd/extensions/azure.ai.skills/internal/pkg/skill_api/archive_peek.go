// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"archive/zip"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	peekMaxSkillMdBytes = 1 << 20 // 1 MiB cap on the SKILL.md we read into memory.
	peekMaxEntries      = 1024    // SKILL.md is at the root; cap deep scans.
)

// PeekArchiveSkillName returns the `name` declared in the archive's SKILL.md
// front matter, or "" when there's no SKILL.md or no `name` claim. Used by
// the destructive `--force` guard: if the archive claims a different name
// than the positional argument, we refuse the delete-then-create.
//
// Looks for SKILL.md at the archive root or one directory below.
// ZIP-only — the upload surface is ZIP. The caller passes an io.ReaderAt
// + size (typically an *os.File and its stat size) so the archive is not
// slurped into memory: zip.NewReader streams central-directory and entry
// payloads via ReadAt as needed.
func PeekArchiveSkillName(r io.ReaderAt, size int64) (string, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidArchive, err)
	}

	for i, entry := range zr.File {
		if i >= peekMaxEntries {
			return "", nil
		}
		if !entry.Mode().IsRegular() || !isSkillMdEntry(entry.Name) {
			continue
		}
		rc, openErr := entry.Open()
		if openErr != nil {
			return "", nil
		}
		raw, readErr := io.ReadAll(io.LimitReader(rc, peekMaxSkillMdBytes))
		_ = rc.Close()
		if readErr != nil {
			return "", nil
		}
		md, parseErr := ParseSkillMd(raw)
		if parseErr != nil {
			// Malformed SKILL.md — let the server reject the upload; the
			// --force guard only fires on an unambiguous name mismatch.
			return "", nil
		}
		return md.Name, nil
	}
	return "", nil
}

func isSkillMdEntry(name string) bool {
	cleaned := path.Clean(strings.TrimLeft(name, "/"))
	if cleaned == "SKILL.md" {
		return true
	}
	dir, file := path.Split(cleaned)
	if file != "SKILL.md" {
		return false
	}
	dir = strings.TrimSuffix(dir, "/")
	return dir != "" && !strings.Contains(dir, "/")
}
