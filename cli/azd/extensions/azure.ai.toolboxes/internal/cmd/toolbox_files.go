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
type toolboxConnectionSpec struct {
	Name  string `json:"name" yaml:"name"`
	Index string `json:"index,omitempty" yaml:"index,omitempty"`
}

// toolboxToolsFile is the file shape for `toolbox connection add --from-file`.
//
// Each connections[] item resolves through the project's connections
// data-plane and is converted into a service tool entry. The toolbox's
// existing description and metadata are carried forward; the file does not
// accept `description` (set at create time only in v1).
type toolboxToolsFile struct {
	Connections []toolboxConnectionSpec `json:"connections,omitempty" yaml:"connections,omitempty"`
}

// toolboxCreateFile is the file shape for `toolbox create --from-file`.
//
// description is optional and stored on the initial version.
// connections[] is required and lists existing project connections to attach.
type toolboxCreateFile struct {
	Description string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Connections []toolboxConnectionSpec `json:"connections,omitempty" yaml:"connections,omitempty"`
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

// suggestionForParseError returns a context-aware fix-it hint. The common
// surprise is putting `description` in a `connection add` file (the field
// only applies to `create`); call that out explicitly so the user does not
// have to read the file-shape doc to know why their description was rejected.
func suggestionForParseError(out any, err error) string {
	msg := err.Error()
	if _, ok := out.(*toolboxToolsFile); ok && strings.Contains(msg, "description") {
		return "the 'description' field is only accepted by `toolbox create`; " +
			"in v1 a toolbox's description is set at create time and cannot be changed later"
	}
	return "fix the file and retry; see `azd ai toolbox create --help` " +
		"or `azd ai toolbox connection add --help` for the supported file shape"
}
