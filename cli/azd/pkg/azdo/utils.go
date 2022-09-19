package azdo

import (
	"context"
	"fmt"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/environment"
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
func EnsureAzdoPatExists(ctx context.Context, env *environment.Environment) (string, error) {
	return ensureAzdoConfigExists(ctx, env, AzDoPatName, "azure devops personal access token")
}

// helper method to ensure an Azure DevOps organization name exists either in .env or system environment variables
func EnsureAzdoOrgNameExists(ctx context.Context, env *environment.Environment) (string, error) {
	return ensureAzdoConfigExists(ctx, env, AzDoEnvironmentOrgName, "azure devops organization name")
}
