// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package add

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal/names"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

const (
	StorageDataTypeBlob = "Blobs"
)

func allStorageDataTypes() []string {
	return []string{StorageDataTypeBlob}
}

func fillStorageDetails(
	ctx context.Context,
	r *project.ResourceConfig,
	console input.Console,
	p PromptOptions) (*project.ResourceConfig, error) {
	r.Name = "storage"

	if _, exists := p.PrjConfig.Resources["storage"]; exists {
		return nil, fmt.Errorf("only one Storage resource is allowed at this time")
	}

	modelProps, ok := r.Props.(project.StorageProps)
	if !ok {
		return nil, fmt.Errorf("invalid resource properties")
	}

	selectedDataTypes, err := selectStorageDataTypes(ctx, console)
	if err != nil {
		return nil, err
	}

	for _, option := range selectedDataTypes {
		switch option {
		case StorageDataTypeBlob:
			if err := fillBlobDetails(ctx, console, &modelProps); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unsupported data type: %s", option)
		}
	}

	r.Props = modelProps
	return r, nil
}

func selectStorageDataTypes(ctx context.Context, console input.Console) ([]string, error) {
	var selectedDataOptions []string
	for {
		var err error
		selectedDataOptions, err = console.MultiSelect(ctx, input.ConsoleOptions{
			Message:      "What type of data do you want to store?",
			Options:      allStorageDataTypes(),
			DefaultValue: []string{StorageDataTypeBlob},
		})
		if err != nil {
			return nil, err
		}

		if len(selectedDataOptions) == 0 {
			console.Message(ctx, output.WithErrorFormat("At least one data type must be selected"))
			continue
		}
		break
	}
	return selectedDataOptions, nil
}

func fillBlobDetails(ctx context.Context, console input.Console, modelProps *project.StorageProps) error {
	for {
		containerName, err := console.Prompt(ctx, input.ConsoleOptions{
			Message: "Input a blob container name to be created:",
			Help: "Blob container name\n\n" +
				"A blob container organizes a set of blobs, similar to a directory in a file system.",
		})
		if err != nil {
			return err
		}

		if err := validateContainerName(containerName); err != nil {
			console.Message(ctx, err.Error())
			continue
		}
		modelProps.Containers = append(modelProps.Containers, containerName)
		break
	}
	return nil
}

// validateContainerName validates storage account container names.
// Reference:
// https://learn.microsoft.com/rest/api/storageservices/naming-and-referencing-containers--blobs--and-metadata
func validateContainerName(name string) error {
	if len(name) < 3 {
		return errors.New("name must be 3 characters or more")
	}

	if strings.Contains(name, "--") {
		return errors.New("name cannot contain consecutive hyphens")
	}

	if strings.ToLower(name) != name {
		return errors.New("name must be all lower case")
	}

	err := names.ValidateLabelName(name)
	if err != nil {
		return err
	}

	return nil
}
