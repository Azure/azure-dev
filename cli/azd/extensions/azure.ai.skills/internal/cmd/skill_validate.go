// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"regexp"
	"strings"

	"azureaiskills/internal/exterrors"
)

// skillNamePattern is the fallback skill name regex used when the service
// does not publish a separate constraint. Matches `agent_yaml.ValidateAgentName`
// in `azure.ai.agents` so users see one consistent rule across resource kinds.
//
//   - Must start with an alphanumeric character.
//   - May contain alphanumerics and hyphens in the middle.
//   - Must end with an alphanumeric character (when more than 1 character).
//   - Length 1-63 (matches the service's @maxLength on `name`).
var skillNamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// validateSkillName returns a structured validation error when name does not
// satisfy [skillNamePattern]. The service has the final say; this function is
// a fast-fail guard to avoid round-tripping obviously invalid names.
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
