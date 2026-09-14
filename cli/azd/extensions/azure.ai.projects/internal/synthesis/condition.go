// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// evaluateCondition matches project.ServiceConfig.IsEnabled.
// Foundry expressions are not valid in condition values.
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
	expanded, err := osutil.NewExpandableString(value).Envsubst(getenv)
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
