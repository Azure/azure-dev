// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"slices"
	"strings"
)

// ConfigValidationError is returned when the azure.yaml configuration contains
// structural problems such as nil service, resource, or hook definitions.
// Callers can use [errors.As] to programmatically inspect the individual Issues.
type ConfigValidationError struct {
	Issues []string
}

func (e *ConfigValidationError) Error() string {
	return fmt.Sprintf("azure.yaml contains invalid configuration:\n  - %s", strings.Join(e.Issues, "\n  - "))
}

// validateParsedConfig checks a freshly-parsed ProjectConfig for nil service, resource, and
// hook definitions that would cause nil pointer dereference panics during subsequent processing.
// YAML sections like "web:" with only a comment or whitespace unmarshal as nil map entries,
// which would cause nil pointer dereference panics during subsequent processing.
//
// The function validates Services, Resources, and Hooks at both project and service levels.
// All problems are collected and returned in a single error so the user can fix them at once.
func validateParsedConfig(config *ProjectConfig) error {
	var problems []string

	for key, svc := range config.Services {
		if svc == nil {
			problems = append(problems, fmt.Sprintf(
				"service '%s' has an empty definition;"+
					" expected properties such as host, language, and project",
				key,
			))
			continue
		}

		problems = append(problems, validateHooks(svc.Hooks, "service '"+key+"'")...)
	}

	for key, res := range config.Resources {
		if res == nil {
			problems = append(problems,
				fmt.Sprintf("resource '%s' has an empty definition; expected properties such as type", key))
		}
	}

	problems = append(problems, validateHooks(config.Hooks, "")...)

	if len(problems) > 0 {
		// Sort for deterministic output regardless of map iteration order.
		slices.Sort(problems)

		return &ConfigValidationError{Issues: problems}
	}

	return nil
}

// validateHooks checks a HooksConfig for nil entries. When scope is non-empty it is
// prepended to each problem description to identify the parent (e.g., "service 'web'").
func validateHooks(hooks HooksConfig, scope string) []string {
	var problems []string

	prefix := ""
	if scope != "" {
		prefix = scope + " "
	}

	for hookName, hookList := range hooks {
		if hookList == nil {
			problems = append(problems, fmt.Sprintf(
				"%shook '%s' has an empty definition;"+
					" expected properties such as run or shell",
				prefix, hookName,
			))
			continue
		}

		for i, hook := range hookList {
			if hook == nil {
				problems = append(problems,
					fmt.Sprintf("%shook '%s' entry %d has an empty definition; expected properties such as run or shell",
						prefix, hookName, i+1))
			}
		}
	}

	return problems
}
