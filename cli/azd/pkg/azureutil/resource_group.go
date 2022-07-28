// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"

)

// GetResourceGroupsForDeployment returns the names of all the resource groups from a subscription level deployment.
func GetResourceGroupsForDeployment(ctx context.Context, azCli tools.AzCli, subscriptionId string, deploymentName string) ([]string, error) {
	deployment, err := azCli.GetSubscriptionDeployment(ctx, subscriptionId, deploymentName)
	if err != nil {
		return nil, fmt.Errorf("fetching current deployment: %w", err)
	}

	// NOTE: it's possible for a deployment to list a resource group more than once. We're only interested in the
	// unique set.
	resourceGroups := map[string]struct{}{}

	for _, dependency := range deployment.Properties.Dependencies {
		for _, dependent := range dependency.DependsOn {
			if dependent.ResourceType == string(infra.AzureResourceTypeResourceGroup) {
				resourceGroups[dependent.ResourceName] = struct{}{}
			}
		}
	}

	var keys []string

	for k := range resourceGroups {
		keys = append(keys, k)
	}

	return keys, nil
}

// GetResourceGroupsForEnvironment gets all resources groups for a given environment
func GetResourceGroupsForEnvironment(ctx context.Context, env *environment.Environment) ([]tools.AzCliResource, error) {
	azCli := commands.GetAzCliFromContext(ctx)
	query := fmt.Sprintf(`resourceContainers 
		| where type == "microsoft.resources/subscriptions/resourcegroups" 
		| where tags['azd-env-name'] == '%s' 
		| project id, name, type, tags, location`,
		strings.ToLower(env.GetEnvName()))

	queryResult, err := azCli.GraphQuery(ctx, query, []string{env.GetSubscriptionId()})

	if err != nil {
		return nil, fmt.Errorf("executing graph query: %s:%w", query, err)
	}

	return queryResult.Data, nil
}
