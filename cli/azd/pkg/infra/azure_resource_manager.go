// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/azureutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type AzureResourceManager struct {
	resourceService   *azapi.ResourceService
	deploymentService *azapi.StandardDeployments
}

type ResourceManager interface {
	WalkDeploymentOperations(
		ctx context.Context,
		deployment Deployment,
		fn WalkDeploymentOperationFunc,
	) error
	GetResourceTypeDisplayName(
		ctx context.Context,
		subscriptionId string,
		resourceId string,
		resourceType azapi.AzureResourceType,
	) (string, error)
	GetResourceGroupsForEnvironment(
		ctx context.Context,
		subscriptionId string,
		envName string,
	) ([]*azapi.Resource, error)
	FindResourceGroupForEnvironment(
		ctx context.Context,
		subscriptionId string,
		envName string,
	) (string, error)
}

// WalkDeploymentOperationFunc is invoked for each valid deployment operation encountered during traversal.
//
// Returning a non-nil error will halt the traversal and return that error.
// Returning SkipExpand() will prevent traversal of any nested deployments within the current operation,
// but will continue traversal of sibling operations.
type WalkDeploymentOperationFunc func(ctx context.Context, operation *armresources.DeploymentOperation) error

// errSkipExpand is the unexported sentinel error used to skip expanding nested deployments.
var errSkipExpand = errors.New("skip deployment expansion")

// SkipExpand returns the sentinel error used to skip ARM resource expansion.
// Return this from a [WalkDeploymentOperationFunc] to prevent traversal of
// nested deployments within the current operation.
func SkipExpand() error { return errSkipExpand }

// IsSkipExpand reports whether err is the skip-expand sentinel.
func IsSkipExpand(err error) bool { return errors.Is(err, errSkipExpand) }

// maxConcurrentDeploymentFetches limits concurrent ARM API calls when fetching nested deployment operations.
const maxConcurrentDeploymentFetches = 10

func NewAzureResourceManager(
	resourceService *azapi.ResourceService,
	deploymentService *azapi.StandardDeployments,
) ResourceManager {
	return &AzureResourceManager{
		resourceService:   resourceService,
		deploymentService: deploymentService,
	}
}

// WalkDeploymentOperations traverses deployment operations and allows callers to skip nested expansion.
func (rm *AzureResourceManager) WalkDeploymentOperations(
	ctx context.Context,
	deployment Deployment,
	fn WalkDeploymentOperationFunc,
) error {
	rootDeploymentOperations, err := deployment.Operations(ctx)
	if err != nil {
		return fmt.Errorf("getting root deployment operations: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan *arm.ResourceID, maxConcurrentDeploymentFetches)
	results := make(chan []*armresources.DeploymentOperation, maxConcurrentDeploymentFetches)
	errCh := make(chan error, 1)

	var workers sync.WaitGroup

	// shutdownWorkers closes the jobs channel and waits for all workers to exit,
	// draining the results channel in parallel to prevent deadlock in case a worker
	// is blocked trying to send a result.
	shutdownWorkers := func() {
		close(jobs)
		done := make(chan struct{})
		go func() {
			workers.Wait()
			close(done)
		}()
		for {
			select {
			case <-done:
				return
			case <-results:
				// drain pending results to unblock workers
			}
		}
	}

	worker := func() {
		defer workers.Done()

		for resourceID := range jobs {
			operations, err := rm.fetchNestedOperations(ctx, resourceID)
			if err != nil {
				select {
				case errCh <- fmt.Errorf("getting deployment operations recursively: %w", err):
				default:
				}
				cancel()
				return
			}

			select {
			case <-ctx.Done():
				return
			case results <- operations:
			}
		}
	}

	for range maxConcurrentDeploymentFetches {
		workers.Add(1)
		go worker()
	}

	queueNestedDeployments := func(
		queue []*arm.ResourceID,
		operations []*armresources.DeploymentOperation,
	) ([]*arm.ResourceID, error) {
		for _, operation := range operations {
			if operation.ID == nil || operation.Properties == nil {
				continue
			}

			if fn != nil {
				// invoke the walk function for every deployment operation
				walkErr := fn(ctx, operation)
				if IsSkipExpand(walkErr) {
					continue
				} else if walkErr != nil {
					return nil, walkErr
				}
			}

			// handle nested deployments
			if !isNestedDeployment(operation) {
				continue
			}
			if operation.Properties.TargetResource == nil || operation.Properties.TargetResource.ID == nil {
				continue
			}

			resourceID, err := arm.ParseResourceID(*operation.Properties.TargetResource.ID)
			if err != nil {
				return nil, fmt.Errorf("parsing deployment resource ID: %w", err)
			}

			queue = append(queue, resourceID)
		}

		return queue, nil
	}

	queue, err := queueNestedDeployments(nil, rootDeploymentOperations)
	if err != nil {
		cancel()
		shutdownWorkers()
		return err
	}

	pending := 0

	// we terminate when there are no more jobs and no more pending operations to fetch
	for pending > 0 || len(queue) > 0 {
		var nextJob *arm.ResourceID
		var jobsCh chan *arm.ResourceID
		if len(queue) > 0 {
			nextJob = queue[0]
			jobsCh = jobs
		}

		select {
		case jobsCh <- nextJob:
			queue = queue[1:]
			pending++
		case <-ctx.Done():
			shutdownWorkers()
			select {
			case walkErr := <-errCh:
				return walkErr
			default:
				return ctx.Err()
			}
		case walkErr := <-errCh:
			shutdownWorkers()
			return walkErr
		case nestedOperations := <-results:
			pending--

			queue, err = queueNestedDeployments(queue, nestedOperations)
			if err != nil {
				cancel()
				shutdownWorkers()
				return err
			}
		}
	}

	// Normal exit: all results consumed (pending == 0), no workers blocked.
	close(jobs)
	workers.Wait()

	select {
	case walkErr := <-errCh:
		return walkErr
	default:
	}

	return nil
}

// GetResourceGroupsForEnvironment gets all resources groups for a given environment
func (rm *AzureResourceManager) GetResourceGroupsForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) ([]*azapi.Resource, error) {
	res, err := rm.resourceService.ListResourceGroup(ctx, subscriptionId, &azapi.ListResourceGroupOptions{
		TagFilter: &azapi.Filter{Key: azure.TagKeyAzdEnvName, Value: envName},
	})

	if err != nil {
		return nil, err
	}

	if len(res) == 0 {
		return nil, azureutil.ResourceNotFound(
			fmt.Errorf("0 resource groups with tag '%s' with value: '%s'", azure.TagKeyAzdEnvName, envName),
		)
	}

	return res, nil
}

// GetDefaultResourceGroups gets the default resource groups regardless of azd-env-name setting
// azd initially released with {envname}-rg for a default resource group name.  We now don't hardcode the default
// We search for them instead using the rg- prefix or -rg suffix
func (rm *AzureResourceManager) GetDefaultResourceGroups(
	ctx context.Context,
	subscriptionId string,
	environmentName string,
) ([]*azapi.Resource, error) {
	allGroups, err := rm.resourceService.ListResourceGroup(ctx, subscriptionId, nil)

	matchingGroups := []*azapi.Resource{}
	for _, group := range allGroups {
		if group.Name == fmt.Sprintf("rg-%[1]s", environmentName) ||
			group.Name == fmt.Sprintf("%[1]s-rg", environmentName) {
			matchingGroups = append(matchingGroups, group)
		}
	}

	if err != nil {
		return nil, err
	}

	if len(matchingGroups) == 0 {
		return nil, azureutil.ResourceNotFound(
			fmt.Errorf("0 resource groups with prefix or suffix with value: '%s'", environmentName),
		)
	}

	return matchingGroups, nil
}

// FindResourceGroupForEnvironment will search for the resource group associated with an environment
// It will first try to find a resource group tagged with azd-env-name
// Then it will try to find a resource group that defaults to either {envname}-rg or rg-{envname}
// If it finds exactly one resource group, then it will use it
// If it finds more than one or zero resource groups, then it will prompt the user to update azure.yaml or
// AZURE_RESOURCE_GROUP
// with the resource group to use.
func (rm *AzureResourceManager) FindResourceGroupForEnvironment(
	ctx context.Context,
	subscriptionId string,
	envName string,
) (string, error) {
	// Let's first try to find the resource group by environment name tag (azd-env-name)
	rgs, err := rm.GetResourceGroupsForEnvironment(ctx, subscriptionId, envName)
	if _, ok := errors.AsType[*azureutil.ResourceNotFoundError](err); err != nil && !ok {
		return "", fmt.Errorf("getting resource group for environment: %s: %w", envName, err)
	}
	// Several Azure resources can create managed resource groups automatically. Here are a few examples:
	// - Azure Kubernetes Service (AKS)
	// - Azure Data Factory
	// - Azure Machine Learning
	// - Azure Synapse Analytics
	// Managed resource groups are created with the same tag as the environment name, leading azd to think there are
	// multiple resource groups for the environment. We need to filter them out.
	// We do this by checking if the resource group is managed by a resource.
	rgs = slices.DeleteFunc(rgs, func(r *azapi.Resource) bool {
		return r.ManagedBy != nil
	})

	if len(rgs) == 0 {
		// We didn't find any Resource Groups for the environment, now let's try to find Resource Groups with the
		// rg-{envname} prefix or {envname}-rg suffix
		rgs, err = rm.GetDefaultResourceGroups(ctx, subscriptionId, envName)
		if err != nil {
			return "", fmt.Errorf("getting default resource groups for environment: %s: %w", envName, err)
		}
	}

	if len(rgs) == 1 && len(rgs[0].Name) > 0 {
		// We found one and only one RG, so we'll use it.
		return rgs[0].Name, nil
	}

	var findErr error
	if len(rgs) > 1 {
		// We found more than one RG
		findErr = errors.New("more than one possible resource group was found")
	} else {
		// We didn't find any RGs
		findErr = errors.New("unable to find the environment resource group")
	}

	suggestion := "Suggestion: explicitly set the AZURE_RESOURCE_GROUP environment variable or specify " +
		"your resource group in azure.yaml:\n\n" +
		"resourceGroup: your-resource-group\n" +
		"# or for a specific service\n" +
		"services:\n" +
		"  your-service:\n" +
		output.WithSuccessFormat("    resourceGroup: your-resource-group")

	return "", &internal.ErrorWithSuggestion{
		Err:        findErr,
		Suggestion: suggestion,
	}
}

func (rm *AzureResourceManager) GetResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
	resourceType azapi.AzureResourceType,
) (string, error) {
	if resourceType == azapi.AzureResourceTypeWebSite {
		// Web apps have different kinds of resources sharing the same resource type 'Microsoft.Web/sites', i.e. Function app
		// vs. App service It is extremely important that we display the right one, thus we resolve it by querying the
		// properties of the ARM resource.
		resourceTypeDisplayName, err := rm.getWebAppResourceTypeDisplayName(ctx, subscriptionId, resourceId)

		if err != nil {
			return "", err
		} else {
			return resourceTypeDisplayName, nil
		}
	} else if resourceType == azapi.AzureResourceTypeCognitiveServiceAccount {
		resourceTypeDisplayName, err := rm.getCognitiveServiceResourceTypeDisplayName(ctx, subscriptionId, resourceId)

		if err != nil {
			return "", err
		} else {
			return resourceTypeDisplayName, nil
		}
	} else if resourceType == azapi.AzureResourceTypeRedisEnterprise {
		resourceTypeDisplayName, err := rm.getRedisEnterpriseResourceTypeDisplayName(ctx, subscriptionId, resourceId)

		if err != nil {
			return "", err
		} else {
			return resourceTypeDisplayName, nil
		}
	} else {
		resourceTypeDisplayName := azapi.GetResourceTypeDisplayName(resourceType)
		return resourceTypeDisplayName, nil
	}
}

// webAppApiVersion is the API Version we use when querying information about Web App resources
const webAppApiVersion = "2021-03-01"

func (rm *AzureResourceManager) getWebAppResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
) (string, error) {
	resource, err := rm.resourceService.GetResource(ctx, subscriptionId, resourceId, webAppApiVersion)

	if err != nil {
		return "", fmt.Errorf("getting web app resource type display names: %w", err)
	}

	if strings.Contains(resource.Kind, "functionapp") {
		return "Function App", nil
	} else if strings.Contains(resource.Kind, "app") {
		return "App Service", nil
	} else {
		return "Web App", nil
	}
}

// cognitiveServiceApiVersion is the API Version we use when querying information about Cognitive Service resources
const cognitiveServiceApiVersion = "2021-04-30"

func (rm *AzureResourceManager) getCognitiveServiceResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
) (string, error) {
	resource, err := rm.resourceService.GetResource(ctx, subscriptionId, resourceId, cognitiveServiceApiVersion)

	if err != nil {
		return "", fmt.Errorf("getting cognitive service resource type display names: %w", err)
	}

	if strings.Contains(resource.Kind, "OpenAI") {
		return "Azure OpenAI", nil
	} else if strings.Contains(resource.Kind, "FormRecognizer") {
		return "Document Intelligence", nil
	} else if strings.Contains(resource.Kind, "AIHub") {
		return "Foundry", nil
	} else if strings.Contains(resource.Kind, "AIServices") {
		return "Foundry", nil
	} else {
		return "Azure AI Services", nil
	}
}

// redisEnterpriseApiVersion is the API Version we use when querying information about Redis Enterprise resources
const redisEnterpriseApiVersion = "2025-07-01"

func (rm *AzureResourceManager) getRedisEnterpriseResourceTypeDisplayName(
	ctx context.Context,
	subscriptionId string,
	resourceId string,
) (string, error) {
	resource, err := rm.resourceService.GetResource(ctx, subscriptionId, resourceId, redisEnterpriseApiVersion)

	if err != nil {
		return "", fmt.Errorf("getting redis enterprise resource type display names: %w", err)
	}

	if strings.EqualFold(resource.Kind, "v2") {
		return "Azure Managed Redis", nil
	} else {
		return "Redis Enterprise", nil
	}
}

func isNestedDeployment(operation *armresources.DeploymentOperation) bool {
	if operation.Properties.TargetResource == nil ||
		operation.Properties.ProvisioningOperation == nil ||
		operation.Properties.TargetResource.ResourceType == nil {
		return false
	}

	return *operation.Properties.TargetResource.ResourceType == string(azapi.AzureResourceTypeDeployment) &&
		*operation.Properties.ProvisioningOperation == armresources.ProvisioningOperationCreate
}

func isTerminalProvisioningState(state *string) bool {
	if state == nil {
		return false
	}

	switch *state {
	case string(armresources.ProvisioningStateSucceeded),
		string(armresources.ProvisioningStateFailed),
		string(armresources.ProvisioningStateCanceled):
		return true
	default:
		return false
	}
}

func (rm *AzureResourceManager) fetchNestedOperations(
	ctx context.Context,
	resourceID *arm.ResourceID,
) ([]*armresources.DeploymentOperation, error) {
	if resourceID.ResourceGroupName == "" {
		return rm.deploymentService.ListSubscriptionDeploymentOperations(ctx, resourceID.SubscriptionID, resourceID.Name)
	}

	return rm.deploymentService.ListResourceGroupDeploymentOperations(
		ctx,
		resourceID.SubscriptionID,
		resourceID.ResourceGroupName,
		resourceID.Name,
	)
}
