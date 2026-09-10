// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func environmentNotFoundError(environmentName string) error {
	return &azdext.LocalError{
		Message:    fmt.Sprintf("RLE environment %q was not found in this Foundry project.", environmentName),
		Code:       "rle_environment_not_found",
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: "Run azd ai rle list to see the available environments.",
	}
}

func environmentVersionNotFoundError(environmentName string, version string) error {
	return &azdext.LocalError{
		Message: fmt.Sprintf(
			"RLE environment %q with version %q was not found in this Foundry project.",
			environmentName,
			version,
		),
		Code:     "rle_environment_version_not_found",
		Category: azdext.LocalErrorCategoryUser,
		Suggestion: fmt.Sprintf(
			"Run azd ai rle show %s to see the available versions.",
			environmentName,
		),
	}
}
