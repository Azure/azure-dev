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
// connections[] is azd sugar over the project connections data-plane and
// builds the matching tool entry (mcp, azure_ai_search, a2a_preview, or
// connection-backed web_search). tools[] is a verbatim pass-through to the
// data plane's OpenAI.Tool[] shape; use it for connectionless tools (built-in
// web_search, file_search, code_interpreter, ...) or any tool type not yet
// exposed by connections[]. At least one of the two must be non-empty.
type toolboxCreateFile struct {
	Description string                  `json:"description,omitempty" yaml:"description,omitempty"`
	Connections []toolboxConnectionSpec `json:"connections,omitempty" yaml:"connections,omitempty"`
	Tools       []map[string]any        `json:"tools,omitempty"       yaml:"tools,omitempty"`
	Policies    *toolboxPoliciesSpec    `json:"policies,omitempty"    yaml:"policies,omitempty"`
}

// toolboxPoliciesSpec mirrors the data-plane ToolboxPolicies model.
type toolboxPoliciesSpec struct {
	RaiConfig *toolboxRaiConfigSpec `json:"rai_config,omitempty" yaml:"rai_config,omitempty"`
}

// toolboxRaiConfigSpec mirrors the data-plane RaiConfig model.
//
// The wire field per the Foundry TypeSpec is `rai_policy_name`. We also accept
// the friendlier `name` alias and map it onto `rai_policy_name` at validation
// time so existing user docs that use either form keep working.
type toolboxRaiConfigSpec struct {
	RaiPolicyName string `json:"rai_policy_name,omitempty" yaml:"rai_policy_name,omitempty"`
	Name          string `json:"name,omitempty"            yaml:"name,omitempty"`
}

// resolvedPolicyName returns the effective RAI policy name from the spec,
// preferring the wire-shaped `rai_policy_name` over the `name` alias.
// Returns "" if neither is set.
func (r *toolboxRaiConfigSpec) resolvedPolicyName() string {
	if r == nil {
		return ""
	}
	if strings.TrimSpace(r.RaiPolicyName) != "" {
		return strings.TrimSpace(r.RaiPolicyName)
	}
	return strings.TrimSpace(r.Name)
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
