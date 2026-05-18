// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"azureaiskills/internal/exterrors"
)

// skillNamePattern matches the agent name pattern in azure.ai.agents so users
// see one rule across resource kinds: 1-63 alphanumerics with hyphens only
// in the middle. The service makes the final decision.
var skillNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func validateSkillName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillName,
			"skill name must not be empty",
			"pass a non-empty <name> argument",
		)
	}
	if !skillNamePattern.MatchString(trimmed) {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillName,
			fmt.Sprintf("skill name %q is invalid", trimmed),
			"use 1-63 alphanumeric characters; hyphens are allowed only in the middle",
		)
	}
	return nil
}
