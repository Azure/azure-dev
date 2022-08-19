// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azureutil

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/commands"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/sethvargo/go-retry"
)

type ResourceNotFoundError struct {
	err error
}

func (e *ResourceNotFoundError) Error() string {
	if e.err == nil {
		return "resource not found: <nil>"
	}

	return fmt.Sprintf("resource not found: %s", e.err.Error())
}

func ResourceNotFound(err error) error {
	return &ResourceNotFoundError{err: err}
}

// GetResourceGroupsForDeployment returns the names of all the resource groups from a subscription level deployment.
func GetResourceGroupsForDeployment(ctx context.Context, azCli azcli.AzCli, subscriptionId string, deploymentName string) ([]string, error) {
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
func GetResourceGroupsForEnvironment(ctx context.Context, env *environment.Environment) ([]azcli.AzCliResource, error) {
	azCli := commands.GetAzCliFromContext(ctx)
	query := fmt.Sprintf(`resourceContainers 
		| where type == "microsoft.resources/subscriptions/resourcegroups" 
		| where tags['azd-env-name'] == '%s' 
		| project id, name, type, tags, location`,
		env.GetEnvName())

	var graphQueryResults *azcli.AzCliGraphQuery

	err := retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(5*time.Second)), func(ctx context.Context) error {
		queryResult, err := azCli.GraphQuery(ctx, query, []string{env.GetSubscriptionId()})
		if err != nil {
			return fmt.Errorf("executing graph query: %s: %w", query, err)
		}

		if queryResult.Count == 0 {
			notFoundError := ResourceNotFound(errors.New("azure graph query returned 0 results"))
			return retry.RetryableError(notFoundError)
		}

		graphQueryResults = queryResult
		return nil
	})

	if err != nil {
		return nil, err
	}

	return graphQueryResults.Data, nil
}

// GetDefaultResourceGroups gets the default resource groups regardless of azd-env-name setting
// azd initially released with {envname}-rg for a default resource group name.  We now don't hardcode the default
// We search graph for them instead using the rg- prefix or -rg suffix
func GetDefaultResourceGroups(ctx context.Context, env *environment.Environment) ([]azcli.AzCliResource, error) {
	azCli := commands.GetAzCliFromContext(ctx)
	query := fmt.Sprintf(`resourceContainers 
		| where type == "microsoft.resources/subscriptions/resourcegroups" 
		| where name in('rg-%[1]s','%[1]s-rg')
		| project id, name, type, tags, location`,
		env.GetEnvName())

	var graphQueryResults *azcli.AzCliGraphQuery

	err := retry.Do(ctx, retry.WithMaxRetries(10, retry.NewConstant(5*time.Second)), func(ctx context.Context) error {
		queryResult, err := azCli.GraphQuery(ctx, query, []string{env.GetSubscriptionId()})
		if err != nil {
			return fmt.Errorf("executing graph query: %s: %w", query, err)
		}

		if queryResult.Count == 0 {
			notFoundError := ResourceNotFound(errors.New("azure graph query returned 0 results"))
			return retry.RetryableError(notFoundError)
		}

		graphQueryResults = queryResult
		return nil
	})

	if err != nil {
		return nil, err
	}

	return graphQueryResults.Data, nil
}

// FindResourceGroupForEnvironment will search for the resource group associated with an environment
// It will first try to find a resource group tagged with azd-env-name
// Then it will try to find a resource group that defaults to either {envname}-rg or rg-{envname}
// If it finds exactly one resource group, then it will use it
// If it finds more than one or zero resource groups, then it will prompt the user to update azure.yaml or AZURE_RESOURCE_GROUP
// with the resource group to use.
func FindResourceGroupForEnvironment(ctx context.Context, env *environment.Environment) (string, error) {
	// Let's first try to find the resource group by environment name tag (azd-env-name)
	rgs, err := GetResourceGroupsForEnvironment(ctx, env)
	var notFoundError *ResourceNotFoundError
	if err != nil && !errors.As(err, &notFoundError) {
		return "", fmt.Errorf("getting resource group for environment: %s: %w", env.GetEnvName(), err)
	}

	if len(rgs) == 0 {
		// We didn't find any Resource Groups for the environment, now let's try to find Resource Groups with the rg-{envname} prefix or {envname}-rg suffix
		rgs, err = GetDefaultResourceGroups(ctx, env)
		if err != nil {
			return "", fmt.Errorf("getting default resource groups for environment: %s: %w", env.GetEnvName(), err)
		}
	}

	if len(rgs) == 1 && len(rgs[0].Name) > 0 {
		// We found one and only one RG, so we'll use it.
		return rgs[0].Name, nil
	}

	var msg string

	if len(rgs) > 1 {
		// We found more than one RG
		msg = "more than one possible resource group was found."
	} else {
		// We didn't find any RGs
		msg = "unable to find the environment resource group."
	}

	return "", fmt.Errorf("%s please explicitly specify your resource group in azure.yaml or the AZURE_RESOURCE_GROUP environment variable", msg)
}
