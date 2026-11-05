// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
)

type backend interface {
	get(context.Context, string) (armresources.DeploymentExtended, error)
	deploy(
		context.Context, string, armresources.Deployment, grpcbroker.ProgressFunc,
	) (armresources.DeploymentExtended, error)
	preview(
		context.Context, string, armresources.DeploymentWhatIf, grpcbroker.ProgressFunc,
	) ([]*armresources.WhatIfChange, error)
	group(context.Context, string) (armresources.ResourceGroup, error)
	deleteGroup(context.Context, string, grpcbroker.ProgressFunc) error
	deleteDeployment(context.Context, string, grpcbroker.ProgressFunc) error
}

type azureBackend struct {
	subscription string
	deployments  *armresources.DeploymentsClient
	operations   *armresources.DeploymentOperationsClient
	groups       *armresources.ResourceGroupsClient
}

func newBackend(subscription string, credential azcore.TokenCredential, options *arm.ClientOptions) (backend, error) {
	deployments, err := armresources.NewDeploymentsClient(subscription, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Azure deployments client: %w", err)
	}
	groups, err := armresources.NewResourceGroupsClient(subscription, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Azure resource groups client: %w", err)
	}
	operations, err := armresources.NewDeploymentOperationsClient(subscription, credential, options)
	if err != nil {
		return nil, fmt.Errorf("creating Azure deployment operations client: %w", err)
	}
	return &azureBackend{
		subscription: subscription, deployments: deployments, groups: groups, operations: operations,
	}, nil
}

func (a *azureBackend) get(ctx context.Context, name string) (armresources.DeploymentExtended, error) {
	result, err := a.deployments.GetAtSubscriptionScope(ctx, name, nil)
	if err != nil {
		return armresources.DeploymentExtended{}, err
	}
	if result.Properties == nil {
		return armresources.DeploymentExtended{}, errors.New("Azure deployment response has no properties")
	}
	resources := map[string]bool{}
	err = a.collectResources(ctx, "/subscriptions/"+a.subscription, name, resources, map[string]bool{})
	if err != nil {
		return armresources.DeploymentExtended{}, fmt.Errorf("reading deployed resources: %w", err)
	}
	for _, id := range slices.Sorted(maps.Keys(resources)) {
		result.Properties.OutputResources = append(
			result.Properties.OutputResources, &armresources.ResourceReference{ID: new(id)},
		)
	}
	return result.DeploymentExtended, nil
}

func (a *azureBackend) collectResources(
	ctx context.Context, scope, name string, resources, visited map[string]bool,
) error {
	key := strings.ToLower(scope + "/providers/Microsoft.Resources/deployments/" + name)
	if visited[key] {
		return nil
	}
	visited[key] = true
	pager := a.operations.NewListAtScopePager(scope, name, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, operation := range page.Value {
			if operation == nil || operation.Properties == nil || operation.Properties.TargetResource == nil {
				continue
			}
			target := operation.Properties.TargetResource
			if target.ID == nil {
				continue
			}
			resources[*target.ID] = true
			parsed, err := arm.ParseResourceID(*target.ID)
			if err != nil {
				return fmt.Errorf("parsing deployed resource ID: %w", err)
			}
			if strings.EqualFold(parsed.ResourceType.String(), "Microsoft.Resources/deployments") {
				// A prior down attempt may have removed the nested deployment with
				// its group while leaving the subscription record for retry.
				if err := a.collectResources(ctx, parsed.Parent.String(), parsed.Name, resources, visited); err != nil &&
					!isNotFound(err) {
					return err
				}
			}
		}
	}
	return nil
}

func (a *azureBackend) deploy(
	ctx context.Context, name string, request armresources.Deployment, progress grpcbroker.ProgressFunc,
) (armresources.DeploymentExtended, error) {
	poller, err := a.deployments.BeginCreateOrUpdateAtSubscriptionScope(ctx, name, request, nil)
	if err != nil {
		return armresources.DeploymentExtended{}, err
	}
	result, err := poll(ctx, poller, progress, "Deploying generated resources")
	if err != nil {
		return armresources.DeploymentExtended{}, err
	}
	return result.DeploymentExtended, nil
}

func (a *azureBackend) preview(
	ctx context.Context, name string, request armresources.DeploymentWhatIf, progress grpcbroker.ProgressFunc,
) ([]*armresources.WhatIfChange, error) {
	poller, err := a.deployments.BeginWhatIfAtSubscriptionScope(ctx, name, request, nil)
	if err != nil {
		return nil, err
	}
	result, err := poll(ctx, poller, progress, "Previewing generated resources")
	if err != nil {
		return nil, err
	}
	if result.Error != nil {
		return nil, fmt.Errorf("Azure what-if failed: %s", pointerValue(result.Error.Code))
	}
	if result.Properties == nil {
		return nil, errors.New("Azure what-if response has no changes")
	}
	return result.Properties.Changes, nil
}

func (a *azureBackend) group(ctx context.Context, name string) (armresources.ResourceGroup, error) {
	result, err := a.groups.Get(ctx, name, nil)
	return result.ResourceGroup, err
}

func (a *azureBackend) deleteGroup(ctx context.Context, name string, progress grpcbroker.ProgressFunc) error {
	poller, err := a.groups.BeginDelete(ctx, name, nil)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = poll(ctx, poller, progress, "Deleting generated resource group "+name)
	return err
}

func (a *azureBackend) deleteDeployment(ctx context.Context, name string, progress grpcbroker.ProgressFunc) error {
	poller, err := a.deployments.BeginDeleteAtSubscriptionScope(ctx, name, nil)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = poll(ctx, poller, progress, "Deleting deployment record")
	return err
}

func poll[T any](
	ctx context.Context, poller *runtime.Poller[T], progress grpcbroker.ProgressFunc, message string,
) (T, error) {
	for !poller.Done() {
		report(progress, message)
		if _, err := poller.Poll(ctx); err != nil {
			var zero T
			return zero, err
		}
		if !poller.Done() {
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				var zero T
				return zero, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return poller.Result(ctx)
}

func pointerValue[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

func (p *Provider) request(ctx context.Context) (armresources.Deployment, error) {
	if err := p.prepare(ctx); err != nil {
		return armresources.Deployment{}, err
	}
	// Ownership belongs to the submitted deployment, not the cached compiler result.
	// Preview and deploy may both run on the same instance.
	data, err := json.Marshal(p.template)
	if err != nil {
		return armresources.Deployment{}, fmt.Errorf("encoding generated template: %w", err)
	}
	var template map[string]any
	if err := json.Unmarshal(data, &template); err != nil {
		return armresources.Deployment{}, fmt.Errorf("copying generated template: %w", err)
	}
	resources, ok := template["resources"].([]any)
	if !ok {
		return armresources.Deployment{}, errors.New("generated template must contain an ARM resource array")
	}
	groups := 0
	for _, item := range resources {
		resource, ok := item.(map[string]any)
		if !ok {
			return armresources.Deployment{}, errors.New("generated template contains an invalid resource")
		}
		resourceType, _ := resource["type"].(string)
		if strings.EqualFold(resourceType, "Microsoft.Resources/resourceGroups") {
			groups++
			switch tags := resource["tags"].(type) {
			case map[string]any:
				tags[ownerTag] = p.name
			case string:
				if !strings.HasPrefix(tags, "[") || !strings.HasSuffix(tags, "]") {
					return armresources.Deployment{}, errors.New("generated resource group tags are not an ARM expression")
				}
				resource["tags"] = fmt.Sprintf(
					"[union(%s, createObject('%s', '%s'))]", tags[1:len(tags)-1], ownerTag, p.name,
				)
			case nil:
				resource["tags"] = map[string]any{ownerTag: p.name}
			default:
				return armresources.Deployment{}, errors.New("generated resource group has unsupported tags")
			}
		} else if !strings.EqualFold(resourceType, "Microsoft.Resources/deployments") {
			return armresources.Deployment{}, fmt.Errorf("unsupported generated subscription resource type %q", resourceType)
		}
	}
	if groups == 0 {
		return armresources.Deployment{}, errors.New("generated template does not declare a resource group")
	}
	return armresources.Deployment{
		Location: new(p.scope.location),
		Properties: &armresources.DeploymentProperties{
			Mode: new(armresources.DeploymentModeIncremental), Template: template, Parameters: p.params,
		},
		Tags: map[string]*string{
			ownerTag: new(p.name), "azd-project-name": new(p.projectName),
			"azd-env-name": new(p.envName), "azd-layer-name": new(p.options.Name),
		},
	}, nil
}

func whatIfRequest(request armresources.Deployment) armresources.DeploymentWhatIf {
	return armresources.DeploymentWhatIf{
		Location: request.Location,
		Properties: &armresources.DeploymentWhatIfProperties{
			Mode: request.Properties.Mode, Template: request.Properties.Template, Parameters: request.Properties.Parameters,
		},
	}
}

// Deploy uses incremental subscription deployment and refuses to adopt unrelated existing groups.
func (p *Provider) Deploy(ctx context.Context, progress grpcbroker.ProgressFunc) (*azdext.ProvisioningDeployResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.ensureEnv(ctx); err != nil {
		return nil, err
	}
	request, err := p.request(ctx)
	if err != nil {
		return nil, err
	}
	changes, err := p.azure.preview(ctx, p.name, whatIfRequest(request), progress)
	if err != nil {
		return nil, fmt.Errorf("checking resource group ownership before deployment: %w", err)
	}
	groupCount := 0
	for _, change := range changes {
		if change == nil || change.ResourceID == nil {
			continue
		}
		id, err := arm.ParseResourceID(*change.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("parsing what-if resource ID: %w", err)
		}
		if !strings.EqualFold(id.ResourceType.String(), "Microsoft.Resources/resourceGroups") {
			continue
		}
		groupCount++
		if !strings.EqualFold(id.SubscriptionID, p.scope.subscriptionID) {
			return nil, errors.New("generated resource group belongs to another subscription")
		}
		group, err := p.azure.group(ctx, id.ResourceGroupName)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking generated resource group: %w", err)
		}
		if !p.ownsGroup(group) {
			return nil, fmt.Errorf(
				"refusing to adopt existing resource group %q without matching ownership", id.ResourceGroupName,
			)
		}
	}
	if groupCount == 0 {
		return nil, errors.New("what-if did not resolve generated resource group ownership; deployment was not started")
	}
	deployment, err := p.azure.deploy(ctx, p.name, request, progress)
	if err != nil {
		return nil, fmt.Errorf("deploying generated resources: %w", err)
	}
	state, err := deploymentState(deployment)
	if err != nil {
		return nil, err
	}
	parameters := map[string]*azdext.ProvisioningInputParameter{}
	definitions, _ := p.template["parameters"].(map[string]any)
	for _, parameter := range p.metadata {
		definition, _ := definitions[parameter.Name].(map[string]any)
		kind, _ := definition["type"].(string)
		parameters[parameter.Name] = &azdext.ProvisioningInputParameter{Type: kind, Value: parameter.Value}
	}
	return &azdext.ProvisioningDeployResult{Deployment: &azdext.ProvisioningDeployment{
		Outputs: state.State.Outputs, Parameters: parameters,
	}}, nil
}

// Preview returns ARM what-if changes through the SDK's structured preview contract.
func (p *Provider) Preview(
	ctx context.Context, progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningPreviewResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	request, err := p.request(ctx)
	if err != nil {
		return nil, err
	}
	changes, err := p.azure.preview(ctx, p.name, whatIfRequest(request), progress)
	if err != nil {
		return nil, fmt.Errorf("previewing generated resources: %w", err)
	}
	preview := &azdext.ProvisioningDeploymentPreview{}
	for _, change := range changes {
		if change == nil || change.ResourceID == nil || change.ChangeType == nil {
			return nil, errors.New("Azure what-if returned an incomplete resource change")
		}
		id, err := arm.ParseResourceID(*change.ResourceID)
		if err != nil {
			return nil, fmt.Errorf("parsing what-if resource ID: %w", err)
		}
		preview.Changes = append(preview.Changes, &azdext.ProvisioningDeploymentPreviewChange{
			ChangeType: string(*change.ChangeType), ResourceId: *change.ResourceID,
			ResourceType: id.ResourceType.String(), Name: id.Name,
		})
	}
	preview.Summary = fmt.Sprintf("%d resource changes", len(preview.Changes))
	return &azdext.ProvisioningPreviewResult{Preview: preview}, nil
}

// Destroy deletes recorded resource groups and all their contents, matching standard Bicep teardown.
// Linked existing resources in other groups do not make those groups deletion candidates.
func (p *Provider) Destroy(
	ctx context.Context, options *azdext.ProvisioningDestroyOptions, progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDestroyResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if options.GetPurge() {
		return nil, &azdext.LocalError{
			Message: "azure.resources does not support --purge", Code: "unsupported_purge", Category: "user",
			Suggestion: "Omit --purge to retain soft-deleted resources. " +
				"Use separately authored native Bicep infrastructure if you need purge support; " +
				"azd infra gen cannot export resources provider configuration.",
		}
	}
	if err := p.ensureEnv(ctx); err != nil {
		return nil, err
	}
	deployment, err := p.loadDeployment(ctx, "")
	if isNotFound(err) {
		return &azdext.ProvisioningDestroyResult{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading deployment before deletion: %w", err)
	}
	state, err := deploymentState(deployment)
	if err != nil {
		return nil, err
	}
	groups := map[string]bool{}
	groupReferences := 0
	for _, resource := range state.State.Resources {
		id, err := arm.ParseResourceID(resource.Id)
		if err != nil {
			return nil, fmt.Errorf("parsing deployed resource ID: %w", err)
		}
		if !strings.EqualFold(id.ResourceType.String(), "Microsoft.Resources/resourceGroups") {
			continue
		}
		groupReferences++
		if !strings.EqualFold(id.SubscriptionID, p.scope.subscriptionID) {
			return nil, errors.New("refusing to delete resource group in another subscription")
		}
		_, err = p.azure.group(ctx, id.ResourceGroupName)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading resource group before deletion: %w", err)
		}
		groups[id.ResourceGroupName] = true
	}
	if groupReferences == 0 && len(state.State.Resources) != 0 {
		return nil, errors.New(
			"deployment state contains resources but no generated resource group; deletion was not started",
		)
	}
	if !options.GetForce() && len(groups) != 0 {
		confirmed, err := p.client.Prompt().Confirm(ctx, &azdext.ConfirmRequest{Options: &azdext.ConfirmOptions{
			Message: "Delete resource groups " + strings.Join(slices.Sorted(maps.Keys(groups)), ", ") +
				" and ALL resources inside them, including resources not deployed by this layer?",
			DefaultValue: new(false),
		}})
		if err != nil {
			return nil, fmt.Errorf("confirming resource deletion: %w", err)
		}
		if !confirmed.GetValue() {
			return nil, errors.New("resource deletion was declined")
		}
	}
	for _, group := range slices.Sorted(maps.Keys(groups)) {
		if err := p.azure.deleteGroup(ctx, group, progress); err != nil {
			return nil, fmt.Errorf("deleting generated resource group: %w", err)
		}
	}
	// Deleting a deployment record alone never deletes its resources. Delete the record last so failures remain retryable.
	if err := p.azure.deleteDeployment(ctx, p.name, progress); err != nil {
		return nil, fmt.Errorf("deleting deployment record: %w", err)
	}
	p.params, p.metadata = nil, nil
	return &azdext.ProvisioningDestroyResult{
		InvalidatedEnvKeys: slices.Sorted(maps.Keys(state.State.Outputs)),
	}, nil
}
