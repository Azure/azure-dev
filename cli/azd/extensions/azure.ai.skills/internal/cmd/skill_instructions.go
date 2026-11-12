// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

type skillInstructions struct {
	Value  string
	IsFile bool
}

// MarshalJSON preserves inline content that would otherwise be interpreted as a file path.
func (i skillInstructions) MarshalJSON() ([]byte, error) {
	if i.IsFile {
		return json.Marshal(map[string]string{"file": i.Value})
	}
	if isInstructionFilePath(i.Value) {
		return json.Marshal(map[string]string{"inline": i.Value})
	}
	return json.Marshal(i.Value)
}

// UnmarshalJSON accepts legacy strings and explicit inline or file references.
func (i *skillInstructions) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*i = skillInstructions{Value: value, IsFile: isInstructionFilePath(value)}
		return nil
	}

	var source map[string]string
	if err := json.Unmarshal(data, &source); err != nil {
		return fmt.Errorf("instructions must be a string or an object with inline or file: %w", err)
	}
	inline, hasInline := source["inline"]
	file, hasFile := source["file"]
	if len(source) != 1 || (!hasInline && !hasFile) {
		return fmt.Errorf("instructions must specify exactly one of inline or file")
	}
	value := inline
	if hasFile {
		value = file
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("instructions inline text or file path must not be empty")
	}
	*i = skillInstructions{Value: value, IsFile: hasFile}
	return nil
}

func isInstructionFilePath(instructions string) bool {
	value := strings.TrimSpace(instructions)
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	switch strings.ToLower(filepath.Ext(value)) {
	case ".md", ".txt":
		// Directory separators disambiguate paths with spaces from prose such as "Follow README.md".
		return !strings.ContainsAny(value, " \t") || strings.ContainsAny(value, `/\`)
	default:
		return false
	}
}
