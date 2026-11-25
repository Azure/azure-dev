// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package environment resolves environment names through the azd host.
package environment

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ResolveName validates the selected name, using AZD_ENVIRONMENT when it is empty,
// then falling back to the project's default environment. It does not load environment values.
func ResolveName(ctx context.Context, client azdext.EnvironmentServiceClient, selected string) (string, error) {
	if selected == "" {
		selected = os.Getenv("AZD_ENVIRONMENT")
	}

	if selected != "" {
		response, err := client.Get(ctx, &azdext.GetEnvironmentRequest{Name: selected})
		if err != nil {
			return "", fmt.Errorf("getting environment %q: %w", selected, err)
		}
		if response.GetEnvironment().GetName() == "" {
			return "", fmt.Errorf("environment %q returned no name", selected)
		}
		return response.GetEnvironment().GetName(), nil
	}

	current, err := client.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return "", fmt.Errorf("an active azd environment is required; run 'azd env new' to create one: %w", err)
		}
		return "", fmt.Errorf("getting current environment: %w", err)
	}

	name := current.GetEnvironment().GetName()
	if name == "" {
		return "", errors.New("an active azd environment is required; run 'azd env new' to create one")
	}
	return name, nil
}
