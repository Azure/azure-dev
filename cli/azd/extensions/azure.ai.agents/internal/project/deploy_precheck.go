// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
)

var environmentVariableNamePattern = regexp.MustCompile(
	`^[A-Za-z_][A-Za-z0-9_]*$`,
)

func validateEnvironmentVariableNames(
	serviceEnvironment map[string]string,
	agentEnvironment *[]agent_yaml.EnvironmentVariable,
) error {
	invalidNames := map[string]struct{}{}
	for name := range serviceEnvironment {
		if !environmentVariableNamePattern.MatchString(name) {
			invalidNames[name] = struct{}{}
		}
	}
	if agentEnvironment != nil {
		for _, variable := range *agentEnvironment {
			if !environmentVariableNamePattern.MatchString(variable.Name) {
				invalidNames[variable.Name] = struct{}{}
			}
		}
	}

	if len(invalidNames) == 0 {
		return nil
	}

	names := slices.Sorted(maps.Keys(invalidNames))
	quotedNames := make([]string, len(names))
	for i, name := range names {
		quotedNames[i] = strconv.Quote(name)
	}

	noun := "name"
	verb := "is"
	if len(names) > 1 {
		noun = "names"
		verb = "are"
	}

	return exterrors.Validation(
		exterrors.CodeInvalidEnvironmentVariableName,
		fmt.Sprintf(
			"environment variable %s %s %s invalid. Names must start "+
				"with a letter or underscore, and contain only letters, "+
				"digits, or underscores",
			noun,
			strings.Join(quotedNames, ", "),
			verb,
		),
		"rename the invalid environment variables in azure.yaml or "+
			"agent.yaml, then run `azd deploy` again",
	)
}
