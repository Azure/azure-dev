// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"
)

func loadSkillMd(path string) (*skill_api.SkillMd, error) {
	data, err := readFileWithLimit(path)
	if err != nil {
		return nil, err
	}
	parsed, err := skill_api.ParseSkillMd(data)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("failed to parse %s: %s", path, err),
			"ensure the file begins with a YAML front matter block delimited by '---'",
		)
	}
	return parsed, nil
}
