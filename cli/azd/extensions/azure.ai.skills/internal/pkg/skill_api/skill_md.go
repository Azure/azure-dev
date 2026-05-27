// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package skill_api

import (
	"bytes"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SkillMdFileName is the canonical on-disk filename for a SKILL.md document.
const SkillMdFileName = "SKILL.md"

// SkillMd is the parsed form of a SKILL.md document: a YAML front matter block
// delimited by `---` lines, followed by a Markdown body that becomes the
// skill version's `inline_content.instructions`.
type SkillMd struct {
	Name           string
	Description    string
	Metadata       map[string]string
	Instructions   string
	RawFrontMatter map[string]any
}

// ParseSkillMd parses a SKILL.md document. Both `---` delimiters are required.
// Missing or unparsable front matter returns an error suitable for wrapping
// in a structured validation error.
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
	// Strip a single newline after the closing delimiter so the body doesn't
	// start with a blank line the user didn't write.
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
		return nil, fmt.Errorf("SKILL.md front matter is empty")
	}

	out := &SkillMd{
		RawFrontMatter: raw,
		Instructions:   string(data[bodyStart:]),
	}

	if v, ok := raw["name"]; ok {
		s, err := frontMatterString("name", v)
		if err != nil {
			return nil, err
		}
		out.Name = s
	}
	if v, ok := raw["description"]; ok {
		s, err := frontMatterString("description", v)
		if err != nil {
			return nil, err
		}
		out.Description = s
	}
	if v, ok := raw["metadata"]; ok {
		m, err := frontMatterStringMap("metadata", v)
		if err != nil {
			return nil, err
		}
		out.Metadata = m
	}
	return out, nil
}

const frontMatterDelimiter = "---"

// findFrontMatterBounds returns the byte offsets that bracket the YAML body
// (exclusive of the delimiter lines themselves). Leading blank lines are
// allowed.
func findFrontMatterBounds(data []byte) (open, close int, err error) {
	sawOpen := false
	lineOffset := 0
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
