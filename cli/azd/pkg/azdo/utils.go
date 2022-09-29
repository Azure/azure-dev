// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdo

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// helper method to verify that a configuration exists in the .env file or in system environment variables
func ensureConfigExists(ctx context.Context, env *environment.Environment, key string, label string) (string, error) {
	value := env.Values[key]
	if value != "" {
		return value, nil
	}

	value, exists := os.LookupEnv(key)
	if !exists || value == "" {
		return value, fmt.Errorf("%s not found in environment variable %s", label, key)
	}
	return value, nil
}

// helper method to ensure an Azure DevOps PAT exists either in .env or system environment variables
func EnsurePatExists(ctx context.Context, env *environment.Environment, console input.Console) (string, error) {
	value, err := ensureConfigExists(ctx, env, AzDoPatName, "azure devops personal access token")
	if err != nil {
		console.Message(ctx, fmt.Sprintf(
			"You need an %s. Please create a PAT by following the instructions here %s",
			output.WithWarningFormat("Azure DevOps Personal Access Token (PAT)"),
			output.WithLinkFormat("https://aka.ms/azure-dev/azdo-pat")))
		console.Message(ctx, fmt.Sprintf("(%s this prompt by setting the PAT to env var: %s)",
			output.WithWarningFormat("%s", "skip"),
			output.WithHighLightFormat("%s", AzDoPatName)))

		pat, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Personal Access Token (PAT):",
			DefaultValue: "",
		})
		if err != nil {
			return "", fmt.Errorf("asking for pat: %w", err)
		}
		// set the pat as an environment variable for this cmd run
		// note: the scope of this env var is only this shell invocation and won't be available in the caller parent shell
		os.Setenv(AzDoPatName, pat)
		value = pat
	}
	return value, nil
}

// helper method to ensure an Azure DevOps organization name exists either in .env or system environment variables
func EnsureOrgNameExists(ctx context.Context, env *environment.Environment, console input.Console) (string, error) {
	value, err := ensureConfigExists(ctx, env, AzDoEnvironmentOrgName, "azure devops organization name")
	if err != nil {
		orgName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message:      "Please enter an Azure DevOps Organization Name:",
			DefaultValue: "",
		})
		if err != nil {
			return "", fmt.Errorf("asking for new project name: %w", err)
		}

		err = saveEnvironmentConfig(AzDoEnvironmentOrgName, orgName, env)
		if err != nil {
			return "", err
		}

		value = orgName
	}
	return value, nil
}

// helper function to save configuration values to .env file
func saveEnvironmentConfig(key string, value string, env *environment.Environment) error {
	env.Values[key] = value
	err := env.Save()

	if err != nil {
		return err
	}
	return nil
}
