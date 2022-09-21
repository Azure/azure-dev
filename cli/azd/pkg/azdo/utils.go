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
func ensureAzdoConfigExists(ctx context.Context, env *environment.Environment, key string, label string) (string, error) {
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
func EnsureAzdoPatExists(ctx context.Context, env *environment.Environment, console input.Console) (string, error) {
	value, err := ensureAzdoConfigExists(ctx, env, AzDoPatName, "azure devops personal access token")
	if err != nil {
		console.Message(ctx, output.WithWarningFormat("You need an Azure DevOps Personal Access Token (PAT). Please create a PAT by following the instructions here https://aka.ms/azure-dev/azdo-pat"))
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

		idx, err := console.Select(ctx, input.ConsoleOptions{
			Message: fmt.Sprintf("Would you like to PAT to the %s environment file (.env)?", env.GetEnvName()),
			Options: []string{
				"Yes, save this PAT to the current .env file",
				"No, do not store the PAT.",
			},
			DefaultValue: "Yes, save this PAT to the current .env file",
		})
		if err != nil {
			return "", fmt.Errorf("prompting for pat storage: %w", err)
		}

		switch idx {
		// save pat to env file
		case 0:
			err = saveEnvironmentConfig(AzDoPatName, value, env)
			if err != nil {
				return "", err
			}
		// user rejected pat storage, skip
		case 1:
			break
		default:
			panic(fmt.Sprintf("unexpected selection index %d", idx))
		}
	}
	return value, nil
}

// helper method to ensure an Azure DevOps organization name exists either in .env or system environment variables
func EnsureAzdoOrgNameExists(ctx context.Context, env *environment.Environment, console input.Console) (string, error) {
	value, err := ensureAzdoConfigExists(ctx, env, AzDoEnvironmentOrgName, "azure devops organization name")
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
