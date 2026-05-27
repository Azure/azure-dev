// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"

	"gopkg.in/yaml.v3"
)

// toolboxConnectionSpec is one connection-backed tool input.
// For CognitiveSearch connections, Index is required.
// For GroundingWithCustomSearch connections, InstanceName is required.
type toolboxConnectionSpec struct {
	Name         string `json:"name" yaml:"name"`
	Index        string `json:"index,omitempty" yaml:"index,omitempty"`
	InstanceName string `json:"instance_name,omitempty" yaml:"instance_name,omitempty"`
}

// toolboxSkillSpec is one skill reference input for the file shape. Empty
// Version means "use the skill's default version".
type toolboxSkillSpec struct {
	Name    string `json:"name" yaml:"name"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

// toolboxToolsFile is the file shape for `toolbox connection add --from-file`.
// Description and skills are not accepted here; use `skill add`/`skill remove`
// to change skills, and set description at create time.
type toolboxToolsFile struct {
	Connections []toolboxConnectionSpec `json:"connections,omitempty" yaml:"connections,omitempty"`
}

// toolboxCreateFile is the file shape for `toolbox create --from-file`.
type toolboxCreateFile struct {
	Description string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Connections []toolboxConnectionSpec `json:"connections,omitempty" yaml:"connections,omitempty"`
	Skills      []toolboxSkillSpec      `json:"skills,omitempty"      yaml:"skills,omitempty"`
}

// parseToolboxFile reads a JSON or YAML file into out. Unknown fields are
// rejected so a user typo (e.g. putting `description` in a `connection add`
// file, or misspelling `connections`) produces a sharp local error rather
// than being silently dropped.
func parseToolboxFile(path string, out any) error {
	// #nosec G304 -- reading a user-supplied path is the intent of --from-file
	content, err := os.ReadFile(path)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("failed to read file %q: %v", path, err),
			"verify the file path and permissions, then retry",
		)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		dec := json.NewDecoder(bytes.NewReader(content))
		dec.DisallowUnknownFields()
		if err := dec.Decode(out); err != nil {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("invalid JSON in %q: %v", path, err),
				suggestionForParseError(out, err),
			)
		}
		return nil
	case ".yaml", ".yml":
		dec := yaml.NewDecoder(bytes.NewReader(content))
		dec.KnownFields(true)
		if err := dec.Decode(out); err != nil {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("invalid YAML in %q: %v", path, err),
				suggestionForParseError(out, err),
			)
		}
		return nil
	default:
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unsupported file type %q", ext),
			"use a .json, .yaml, or .yml file",
		)
	}
}

// suggestionForParseError returns a context-aware fix-it hint for common
// shape mistakes (e.g. putting `description` or `skills` in a `connection add`
// file).
func suggestionForParseError(out any, err error) string {
	msg := err.Error()
	if _, ok := out.(*toolboxToolsFile); ok {
		switch {
		case strings.Contains(msg, "description"):
			return "the 'description' field is only accepted by `toolbox create`; " +
				"in v1 a toolbox's description is set at create time and cannot be changed later"
		case strings.Contains(msg, "skills"):
			return "the 'skills' field is only accepted by `toolbox create`; " +
				"skills attached at create time are carried forward across `connection add`/`remove` automatically"
		}
	}
	return "fix the file and retry; see `azd ai toolbox create --help` " +
		"or `azd ai toolbox connection add --help` for the supported file shape"
}
