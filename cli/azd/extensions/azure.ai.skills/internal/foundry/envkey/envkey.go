// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package envkey builds environment keys published by Foundry skill services.
package envkey

import (
	"fmt"
	"strings"
)

// SkillVersion returns the canonical env-var key for a skill's default version.
func SkillVersion(skillName string) string {
	sanitized := strings.ReplaceAll(strings.ToUpper(skillName), "-", "_")
	return fmt.Sprintf("SKILL_%s_VERSION", sanitized)
}

// SkillProjectEndpoint returns the env-var key that scopes a skill deployment
// marker to its Foundry project.
func SkillProjectEndpoint(skillName string) string {
	sanitized := strings.ReplaceAll(strings.ToUpper(skillName), "-", "_")
	return fmt.Sprintf("SKILL_%s_PROJECT_ENDPOINT", sanitized)
}
