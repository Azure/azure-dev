// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package show

import (
	"context"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/braydonk/yaml"

	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/keyvault"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/azure/azure-dev/cli/azd/pkg/yamlnode"
)

type showResource struct {
	env             *environment.Environment
	kvService       keyvault.KeyVaultService
	resourceService *azapi.ResourceService
	console         input.Console
}

func (s *showResource) showResourceGeneric(
	ctx context.Context,
	id arm.ResourceID,
	opts showResourceOptions) (*ux.ShowResource, error) {
	resourceMeta, resourceId := getResourceMeta(id)
	if resourceMeta == nil {
		return nil, fmt.Errorf("resource type '%s' is not currently supported", id.ResourceType)
	}

	spec, err := s.resourceService.GetRawResource(ctx, resourceId, resourceMeta.ApiVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource %s: %w", id.String(), err)
	}

	var resolveSecret func(name string) (string, error)
	if opts.showSecrets {
		vault := infra.KeyVaultName(s.env)

		resolveSecret = func(name string) (string, error) {
			kvSecret, err := s.kvService.GetKeyVaultSecret(ctx, id.SubscriptionID, vault, name)
			if err != nil {
				return "", fmt.Errorf("failed to get secret %s: %w", name, err)
			}
			return kvSecret.Value, nil
		}
	} else {
		resolveSecret = func(name string) (string, error) {
			return "<secret>", nil
		}
	}

	var resourceNode *yaml.Node
	if opts.resourceSpec == nil {
		resourceNode = &yaml.Node{}
	} else {
		// include 'name' in the yaml
		opts.resourceSpec.IncludeName = true

		node, err := yamlnode.Encode(opts.resourceSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to encode resource spec: %w", err)
		}

		resourceNode = node
	}

	context := scaffold.EvalEnv{
		ResourceSpec: resourceNode,
		ArmResource:  spec,
		VaultSecret:  resolveSecret,
	}
	values, err := scaffold.Eval(resourceMeta.Variables, context)
	if err != nil {
		return nil, fmt.Errorf("expanding variables: %w", err)
	}

	display := id.ResourceType.String()
	if translated := azapi.GetResourceTypeDisplayName(azapi.AzureResourceType(display)); translated != "" {
		display = translated
	}

	showRes := ux.ShowResource{
		Name:        id.Name,
		TypeDisplay: display,
		Variables:   values,
	}

	return &showRes, nil
}

func getResourceMeta(id arm.ResourceID) (*scaffold.ResourceMeta, arm.ResourceID) {
	resourceType := id.ResourceType.String()
	resources := scaffold.Resources

	for _, res := range resources {
		if res.ResourceType == resourceType { // exact match
			return &res, id
		}
	}

	var matched *scaffold.ResourceMeta
	parentLevels := 0
	for _, res := range resources {
		// find longest prefix match
		if strings.HasPrefix(resourceType, res.ResourceType) {
			if matched == nil || len(res.ResourceType) > len(matched.ResourceType) {
				matched = &res
				parentLevels = strings.Count(resourceType[len(res.ResourceType):], "/")
			}
		}
	}

	parentId := &id
	for i := 0; i < parentLevels; i++ {
		if parentId.Parent != nil {
			parentId = parentId.Parent
		}
	}

	return matched, *parentId
}
