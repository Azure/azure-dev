// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func fillCosmosDetails(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	if r.Props != nil {
		return r, nil
	}

	props := project.CosmosDBProps{}
	container := project.CosmosDBContainerProps{}
	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: "Input the container name to store data:",
			Help: "Container name\n\n" +
				"A container that is used to store data. For example, the container named 'products' to store product data.",
		})
		if err != nil {
			return r, err
		}

		if err := names.ValidateLabelName(name); err != nil {
			console.Message(ctx, err.Error())
			continue
		}

		container.Name = name
		break
	}

	for {
		name, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: "Input the partition key:",
			Help: "Container name\n\n" +
				"A container that is used to store data. For example, the container named 'products' to store product data.",
		})
		if err != nil {
			return r, err
		}

		if err := names.ValidateLabelName(name); err != nil {
			console.Message(ctx, err.Error())
			continue
		}

		container.Name = name
		break
	}

	props.Containers = []project.CosmosDBContainerProps{}
	return r, nil
}
