package add

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/internal/scaffold"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func ConfigureExisting(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	if r.Name == "" {
		resourceId, err := arm.ParseResourceID(r.ResourceId)
		if err != nil {
			return nil, err
		}

		for {
			name, err := console.Prompt(ctx, input.ConsoleOptions{
				Message: "What should we call this resource?",
				Help: "This name will be used to identify the resource in your project. " +
					"It will also be used to prefix environment variables by default.",
				DefaultValue: names.LabelName(resourceId.Name),
			})
			if err != nil {
				return nil, err
			}

			if err := names.ValidateLabelName(name); err != nil {
				console.Message(ctx, err.Error())
				continue
			}

			r.Name = name
			break
		}
	}

	_ = getResourceMeta(r.Type.AzureResourceType())

	return r, nil
}

// resourceType returns the resource type for the given Azure resource type.
func resourceType(azureResourceType string) project.ResourceType {
	resourceTypes := project.AllResourceTypes()
	for _, resourceType := range resourceTypes {
		if resourceType.AzureResourceType() == azureResourceType {
			return resourceType
		}
	}

	return project.ResourceType("")
}

func getResourceMeta(resourceType string) scaffold.ResourceMeta {
	for _, res := range scaffold.Resources {
		if res.ResourceType == resourceType {
			return res
		}
	}

	return scaffold.ResourceMeta{}
}
