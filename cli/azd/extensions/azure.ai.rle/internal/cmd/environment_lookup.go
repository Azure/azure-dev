// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

func resolveLatestEnvironmentByName(
	ctx context.Context,
	client *rleClient,
	environmentName string,
) (*environmentResource, error) {
	environment, err := client.getEnvironment(ctx, environmentName)
	if isRleNotFound(err) {
		return nil, environmentNotFoundError(environmentName)
	}
	if err != nil {
		return nil, serviceError(err)
	}
	return environment, nil
}

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
			"RLE environment %q version %q was not found in this Foundry project.",
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

func requireReadyEnvironment(environment *environmentResource, environmentName string) error {
	if environment.DiskImageConversionStatus != diskImageConversionStatusReady {
		return &azdext.LocalError{
			Message: fmt.Sprintf(
				"Environment %q disk image status is %q, expected %q.",
				environmentName,
				environment.DiskImageConversionStatus,
				diskImageConversionStatusReady,
			),
			Code:     "rle_disk_image_not_ready",
			Category: azdext.LocalErrorCategoryUser,
			Suggestion: fmt.Sprintf(
				"Run azd ai rle show %s to inspect the environment details and version history.",
				environmentName,
			),
		}
	}

	return nil
}
