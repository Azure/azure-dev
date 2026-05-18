// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"archive/tar"
	"compress/gzip"
	"errors"
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

// peekMaxEntries caps how many tar entries PeekArchiveSkillName scans before
// giving up. SKILL.md is conventionally at the archive root, so scanning
// every entry of a deeply-nested archive is unnecessary and would let a
// malicious archive stall the CLI.
const peekMaxEntries = 1024

// PeekArchiveSkillName reads the gzipped tar stream from r and returns the
// `name` field declared in the archive's SKILL.md front matter.
//
// Lookup rules (first match wins):
//
//   - A file whose cleaned tar entry name equals `SKILL.md`.
//   - A file whose cleaned tar entry name is `<single-top-level-dir>/SKILL.md`,
//     i.e. a SKILL.md exactly one directory below the archive root. This
//     matches the layout produced by `azd ai skill package`.
//
// Returns ("", nil) when no SKILL.md is found, when SKILL.md exists but does
// not declare a `name`, or when the front matter cannot be parsed. The
// caller is expected to treat an empty result as "no claim", not as an
// error: the destructive `--force` guard only fires when the archive makes
// a name claim that disagrees with the positional argument.
//
// PeekArchiveSkillName never writes to disk and reads at most peekMaxSkillMdBytes
// bytes from any single entry. It returns an error only for unrecoverable
// stream problems (invalid gzip, truncated tar header).
func PeekArchiveSkillName(r io.Reader) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidGzip, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	entryCount := 0
	for {
		hdr, hdrErr := tr.Next()
		if errors.Is(hdrErr, io.EOF) {
			return "", nil
		}
		if hdrErr != nil {
			return "", fmt.Errorf("read tar entry: %w", hdrErr)
		}
		entryCount++
		if entryCount > peekMaxEntries {
			return "", nil
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		if !isSkillMdEntry(hdr.Name) {
			continue
		}
		// Limit how much we read so a malicious archive cannot make us
		// allocate gigabytes for a SKILL.md "file".
		data, readErr := io.ReadAll(io.LimitReader(tr, peekMaxSkillMdBytes))
		if readErr != nil {
			return "", fmt.Errorf("read SKILL.md from archive: %w", readErr)
		}
		md, parseErr := ParseSkillMd(data)
		if parseErr != nil {
			// SKILL.md is present but malformed. The upload itself will fail
			// validation; for the --force guard we treat this as "no claim"
			// so we do not block on a parse error before the service has
			// even seen the archive.
			return "", nil
		}
		return md.Name, nil
	}
}

// isSkillMdEntry reports whether the given tar entry name refers to a
// SKILL.md file at the archive root or exactly one directory below it.
func isSkillMdEntry(name string) bool {
	cleaned := path.Clean(strings.TrimLeft(name, "/"))
	if cleaned == "SKILL.md" {
		return true
	}
	// Match `<dir>/SKILL.md` (exactly one directory deep).
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
