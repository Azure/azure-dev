// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"azureaiskills/internal/exterrors"
)

// skillNamePattern matches the SkillName scalar from the Foundry Skills
// API spec (agentskills.io alignment): lowercase letters / digits / hyphens,
// must not start or end with a hyphen, max 64 chars. The service makes the
// final decision.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)

const skillNameMaxLen = 64

func validateSkillName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillName,
			"skill name must not be empty",
			"pass a non-empty <name> argument",
		)
	}
	if len(trimmed) > skillNameMaxLen || !skillNamePattern.MatchString(trimmed) {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillName,
			fmt.Sprintf("skill name %q is invalid", trimmed),
			"use 1-64 lowercase letters, digits, and hyphens; must not start or end with a hyphen",
		)
	}
	return nil
}
