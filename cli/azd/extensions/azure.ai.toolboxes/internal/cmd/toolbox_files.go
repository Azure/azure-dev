// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
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
// existing description is carried forward; use `toolbox update` to change it.
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

// parseToolboxFile reads a JSON or YAML file into out.
func parseToolboxFile(path string, out any) error {
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
		if err := json.Unmarshal(content, out); err != nil {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("invalid JSON in %q: %v", path, err),
				"fix the file and retry",
			)
		}
		return nil
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(content, out); err != nil {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("invalid YAML in %q: %v", path, err),
				"fix the file and retry",
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
