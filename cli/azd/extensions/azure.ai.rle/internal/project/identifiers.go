// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"regexp"
	"strings"
)

var environmentNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func ValidateEnvironmentName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !environmentNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid environment name %q", name)
	}
	return name, nil
}

func Slug(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteRune('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}
