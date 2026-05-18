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

// peekMaxSkillMdBytes caps how much SKILL.md data is read into memory by
// PeekArchiveSkillName. SKILL.md files are expected to be small; a 1 MB cap
// protects against an archive that names a huge file `SKILL.md` and tries to
// exhaust memory during the front-matter peek.
const peekMaxSkillMdBytes = 1 << 20 // 1 MiB

// peekMaxEntries caps how many zip entries PeekArchiveSkillName scans before
// giving up. SKILL.md is conventionally at the archive root, so scanning
// every entry of a deeply-nested archive is unnecessary and would let a
// malicious archive stall the CLI.
const peekMaxEntries = 1024

// PeekArchiveSkillName reads the ZIP archive in data and returns the `name`
// field declared in the archive's SKILL.md front matter.
//
// Lookup rules (first match wins):
//
//   - A file whose cleaned entry name equals `SKILL.md`.
//   - A file whose cleaned entry name is `<single-top-level-dir>/SKILL.md`,
//     i.e. a SKILL.md exactly one directory below the archive root.
//
// Returns ("", nil) when no SKILL.md is found, when SKILL.md exists but does
// not declare a `name`, or when the front matter cannot be parsed. The caller
// is expected to treat an empty result as "no claim", not as an error: the
// destructive `--force` guard only fires when the archive makes a name claim
// that disagrees with the positional argument.
//
// PeekArchiveSkillName accepts only ZIP archives — the upload surface is
// ZIP-only, so this is the only format relevant to the `--force` guard.
func PeekArchiveSkillName(data []byte) (string, error) {
	zr, err := zip.NewReader(newBytesReaderAt(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidArchive, err)
	}

	for i, entry := range zr.File {
		if i >= peekMaxEntries {
			return "", nil
		}
		if !entry.Mode().IsRegular() {
			continue
		}
		if !isSkillMdEntry(entry.Name) {
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
			// SKILL.md is present but malformed. The upload itself will fail
			// validation; for the --force guard we treat this as "no claim"
			// so we do not block on a parse error before the service has
			// even seen the archive.
			return "", nil
		}
		return md.Name, nil
	}
	return "", nil
}

// isSkillMdEntry reports whether the given zip entry name refers to a
// SKILL.md file at the archive root or exactly one directory below it.
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
	if dir == "" || strings.Contains(dir, "/") {
		return false
	}
	return true
}
