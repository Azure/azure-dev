// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SkillMd represents the parsed contents of a SKILL.md file.
//
// SKILL.md files are Markdown files with a YAML front matter block delimited
// by three-dash lines. The CLI extracts the structured fields it knows about
// (Name, Description, Metadata) and uses the remaining Markdown body as the
// skill's `instructions`. Any unrecognized front matter keys are preserved
// verbatim in RawFrontMatter so callers can forward them to the service.
type SkillMd struct {
	// Name is the optional `name` value from the YAML front matter. If the
	// positional command argument differs, the positional value wins and
	// the caller prints a one-line warning to stderr.
	Name string
	// Description is the optional human-readable summary.
	Description string
	// Metadata is the optional string-to-string map from front matter.
	Metadata map[string]string
	// Instructions is the Markdown body that follows the closing `---`. Leading
	// whitespace and a single trailing newline are preserved verbatim.
	Instructions string
	// RawFrontMatter contains every parsed front-matter key (including those
	// already exposed as named fields). Useful for forwarding unknown
	// service-recognized fields without losing information.
	RawFrontMatter map[string]any
}

// ParseSkillMd reads a SKILL.md document from data and returns its components.
//
// The document must start with a YAML front matter block delimited by lines
// containing only `---`. Both the opening and closing delimiters are
// required. Empty input, missing/unparsable front matter, and YAML errors
// all return a non-nil error that callers should wrap in a structured
// validation error.
func ParseSkillMd(data []byte) (*SkillMd, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("SKILL.md is empty")
	}

	openIdx, closeIdx, err := findFrontMatterBounds(data)
	if err != nil {
		return nil, err
	}

	fmBytes := data[openIdx:closeIdx]
	bodyStart := closeIdx + len(frontMatterDelimiter)
	// Skip a single newline after the closing delimiter so the body does not
	// start with a leading blank line that the user did not write.
	if bodyStart < len(data) {
		if data[bodyStart] == '\r' && bodyStart+1 < len(data) && data[bodyStart+1] == '\n' {
			bodyStart += 2
		} else if data[bodyStart] == '\n' {
			bodyStart++
		}
	}

	var raw map[string]any
	if err := yaml.Unmarshal(fmBytes, &raw); err != nil {
		return nil, fmt.Errorf("parse SKILL.md front matter: %w", err)
	}
	if raw == nil {
		// `---\n---` with no body is technically valid YAML (null document)
		// but useless for skill creation. Treat as missing.
		return nil, fmt.Errorf("SKILL.md front matter is empty")
	}

	out := &SkillMd{
		RawFrontMatter: raw,
		Instructions:   string(data[bodyStart:]),
	}

	if v, ok := raw["name"]; ok {
		s, sErr := frontMatterString("name", v)
		if sErr != nil {
			return nil, sErr
		}
		out.Name = s
	}
	if v, ok := raw["description"]; ok {
		s, sErr := frontMatterString("description", v)
		if sErr != nil {
			return nil, sErr
		}
		out.Description = s
	}
	if v, ok := raw["metadata"]; ok {
		m, mErr := frontMatterStringMap("metadata", v)
		if mErr != nil {
			return nil, mErr
		}
		out.Metadata = m
	}

	return out, nil
}

const frontMatterDelimiter = "---"

// findFrontMatterBounds locates the opening `---` and closing `---` markers
// for the YAML front matter block. The returned indices bracket the YAML body
// (exclusive of the delimiter lines themselves).
//
// The opening delimiter must be the first non-empty line of the file. Any
// leading whitespace lines are skipped to be tolerant of UTF-8 BOMs and
// editor-introduced blank lines.
func findFrontMatterBounds(data []byte) (open, close int, err error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Allow up to 1 MiB per line so giant front matter blocks do not hit the
	// default 64 KiB cap. Skills bodies are bounded by the server; front
	// matter is small, but we set a roomy limit anyway.
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)

	sawOpen := false
	lineOffset := 0
	// We need the byte offset of each line's start, including its newline
	// terminator. bufio.Scanner doesn't expose offsets directly, so we
	// recompute them by tracking how many bytes we've advanced.
	cur := data
	lineNum := 0
	for {
		nl := bytes.IndexByte(cur, '\n')
		var line []byte
		var step int
		if nl < 0 {
			line = cur
			step = len(cur)
		} else {
			line = cur[:nl]
			step = nl + 1
		}
		trimmed := strings.TrimRight(string(line), "\r")
		stripped := strings.TrimSpace(trimmed)
		lineNum++

		if !sawOpen {
			if stripped == "" {
				lineOffset += step
				cur = cur[step:]
				if step == 0 {
					return 0, 0, fmt.Errorf("SKILL.md must begin with a YAML front matter block delimited by '---'")
				}
				continue
			}
			if stripped != frontMatterDelimiter {
				return 0, 0, fmt.Errorf("SKILL.md must begin with a YAML front matter block delimited by '---' (got %q on line %d)", trimmed, lineNum)
			}
			sawOpen = true
			open = lineOffset + step
			lineOffset += step
			cur = cur[step:]
			continue
		}

		if stripped == frontMatterDelimiter {
			close = lineOffset
			return open, close, nil
		}

		lineOffset += step
		cur = cur[step:]
		if step == 0 {
			break
		}
	}

	return 0, 0, fmt.Errorf("SKILL.md front matter is missing its closing '---' delimiter")
}

func frontMatterString(field string, v any) (string, error) {
	switch typed := v.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	default:
		return "", fmt.Errorf("SKILL.md front matter field %q must be a string", field)
	}
}

func frontMatterStringMap(field string, v any) (map[string]string, error) {
	if v == nil {
		return nil, nil
	}
	raw, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("SKILL.md front matter field %q must be a mapping", field)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(raw))
	for k, val := range raw {
		s, err := frontMatterString(field+"."+k, val)
		if err != nil {
			return nil, err
		}
		out[k] = s
	}
	return out, nil
}
