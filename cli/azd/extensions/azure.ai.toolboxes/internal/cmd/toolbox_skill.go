// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
)

// skillNamePattern matches the SkillName scalar in the Foundry Skills spec:
// lowercase letters / digits / hyphens, must not start or end with a hyphen,
// max 64 chars. Duplicated from azure.ai.skills' validateSkillName because the
// extensions are separate Go modules; keep both in lockstep if the scalar
// changes.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)

const skillNameMaxLen = 64

// skillSpec is the parsed form of a --skill or skills[] entry. Empty Version
// means "use the skill's default version" per the ToolboxSkillReference
// contract.
type skillSpec struct {
	Name    string
	Version string
}

// parseSkillFlag parses `<name>` or `<name>@<version>`. Version is opaque and
// passed to the service verbatim.
func parseSkillFlag(s string) (skillSpec, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return skillSpec{}, exterrors.Validation(
			exterrors.CodeInvalidSkillSpec,
			"--skill value must not be empty",
			"pass --skill <name>[@<version>]",
		)
	}

	name := trimmed
	version := ""
	if at := strings.IndexByte(trimmed, '@'); at >= 0 {
		name = trimmed[:at]
		version = strings.TrimSpace(trimmed[at+1:])
		if version == "" {
			return skillSpec{}, exterrors.Validation(
				exterrors.CodeInvalidSkillSpec,
				fmt.Sprintf("--skill %q has an empty version after '@'", trimmed),
				"either drop the trailing '@' to use the skill's default version, "+
					"or pass <name>@<version>",
			)
		}
	}

	if err := validateSkillName(name); err != nil {
		return skillSpec{}, err
	}
	return skillSpec{Name: name, Version: version}, nil
}

// validateSkillName enforces the SkillName regex + length cap.
func validateSkillName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillName,
			"skill name must not be empty",
			"pass a non-empty skill name",
		)
	}
	if len(trimmed) > skillNameMaxLen || !skillNamePattern.MatchString(trimmed) {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillName,
			fmt.Sprintf("skill name %q is invalid", trimmed),
			"use 1-64 lowercase letters, digits, and hyphens; "+
				"must not start or end with a hyphen",
		)
	}
	return nil
}

// buildSkillEntry returns the wire map for a ToolboxSkillReference (the only
// ToolboxSkill variant in the spec today).
func buildSkillEntry(spec skillSpec) map[string]any {
	entry := map[string]any{
		"type": "skill_reference",
		"name": spec.Name,
	}
	if spec.Version != "" {
		entry["version"] = spec.Version
	}
	return entry
}

// validateNoDuplicateSkills rejects two skills[] entries with the same name.
// The service may also reject this; the local check produces a sharper error.
func validateNoDuplicateSkills(entries []map[string]any) error {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if n, ok := e["name"].(string); ok && n != "" {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	for i := 1; i < len(names); i++ {
		if names[i] == names[i-1] {
			return exterrors.Validation(
				exterrors.CodeDuplicateSkill,
				fmt.Sprintf("skill %q appears more than once in the input", names[i]),
				"remove duplicate --skill entries (or duplicate skills[] entries in the file)",
			)
		}
	}
	return nil
}
