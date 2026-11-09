// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"

	"azure.ai.projects/synthesis"

	"azureaiagent/internal/exterrors"
)

// environmentReference uses the shared synthesis scanner's spans.
type environmentReference = synthesis.EnvReference

// findEnvironmentReferences validates before discovery so init and
// service env generation see every live expansion.
func findEnvironmentReferences(value string) ([]environmentReference, error) {
	if err := synthesis.ValidateEnvReferences(value); err != nil {
		// Defaults in validator errors can contain credentials.
		return nil, errors.New("unsupported environment variable reference: " +
			"shell operators, nested defaults, and malformed expressions are not supported")
	}
	return synthesis.FindEnvReferences(value), nil
}

func invalidServiceEnvironmentReferences(serviceName string, err error) error {
	return exterrors.Validation(
		exterrors.CodeInvalidServiceConfig,
		fmt.Sprintf("service %q has invalid environment variable references: %s", serviceName, err),
		"Use ${VAR} or ${VAR:-default} with a literal default, "+
			"$${VAR} for literal text, or ${{...}} for Foundry expressions.",
	)
}
