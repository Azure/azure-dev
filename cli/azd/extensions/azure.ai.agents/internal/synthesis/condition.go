// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
)

// evaluateCondition matches project.ServiceConfig.IsEnabled.
// Extensions pin a published azd module, so they cannot import
// newer core helpers until that module is bumped.
func evaluateCondition(
	value string,
	getenv func(string) string,
) (bool, error) {
	if value == "" {
		return true, nil
	}
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	expanded, err := foundry.ExpandEnv(value, getenv)
	if err != nil {
		return false, fmt.Errorf(
			"malformed condition template: %w",
			err,
		)
	}
	return isTruthyCondition(expanded), nil
}

func isTruthyCondition(value string) bool {
	switch value {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes":
		return true
	default:
		return false
	}
}
