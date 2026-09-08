// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/synthesis"
)

func validateEjectedConnectionCredentials(parameters map[string]any) error {
	credentials, ok := parameters["connectionCredentials"]
	if !ok || credentials == nil {
		return nil
	}
	if err := synthesis.ValidateEjectionCredentials(credentials); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("cannot eject concrete connection credentials: %s", err),
			"replace concrete credential values with ${VAR} environment references "+
				"or supported Foundry server-side references, then retry",
		)
	}
	return nil
}
