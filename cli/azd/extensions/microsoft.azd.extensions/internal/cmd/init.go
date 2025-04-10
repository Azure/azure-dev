// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal"
	"github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.extensions/internal/models"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new AZD extension project",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}

			defer azdClient.Close()

			extensionMetadata, err := collectExtensionMetadata(ctx, azdClient)
			if err != nil {
				return fmt.Errorf("failed to collect extension metadata: %w", err)
			}

			if err := createExtensionDirectory(ctx, azdClient, extensionMetadata); err != nil {
				return fmt.Errorf("failed to create extension directory: %w", err)
			}

			return nil
		},
	}
}

func collectExtensionMetadata(ctx context.Context, azdClient *azdext.AzdClient) (*models.ExtensionSchema, error) {
	idPrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Enter a unique identifier for your extension",
			Placeholder:     "company.extension",
			RequiredMessage: "Extension ID is required",
			Required:        true,
			Hint:            "Extension ID is used to identify your extension in the AZD ecosystem. It should be unique and follow the format 'company.extension'.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for extension ID: %w", err)
	}

	displayNamePrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Enter a display name for your extension",
			Placeholder:     "My Extension",
			RequiredMessage: "Display name is required",
			Required:        true,
			HelpMessage:     "Display name is used to show the extension name in the AZD CLI. It should be user-friendly and descriptive.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for display name: %w", err)
	}

	namespacePrompt, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         "Enter a namespace for your extension",
			RequiredMessage: "Namespace is required",
			Required:        true,
			HelpMessage:     "Namespace is used to group custom commands into a single command group used for executing the extension.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for namespace: %w", err)
	}

	capabilitiesPrompt, err := azdClient.Prompt().MultiSelect(ctx, &azdext.MultiSelectRequest{
		Options: &azdext.MultiSelectOptions{
			Message: "Select capabilities for your extension",
			Choices: []*azdext.MultiSelectChoice{
				{
					Label: "Custom Commands",
					Value: "custom-commands",
				},
				{
					Label: "Lifecycle Events",
					Value: "lifecycle-events",
				},
			},
			EnableFiltering: internal.ToPtr(false),
			DisplayNumbers:  internal.ToPtr(false),
			HelpMessage:     "Capabilities define the features and functionalities of your extension. You can select multiple capabilities.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to prompt for capabilities: %w", err)
	}

	capabilities := make([]extensions.CapabilityType, len(capabilitiesPrompt.Values))
	for i, capability := range capabilitiesPrompt.Values {
		capabilities[i] = extensions.CapabilityType(capability.Value)
	}

	return &models.ExtensionSchema{
		Id:           idPrompt.Value,
		DisplayName:  displayNamePrompt.Value,
		Namespace:    namespacePrompt.Value,
		Capabilities: capabilities,
		Usage:        fmt.Sprintf("azd %s <command> [options]", namespacePrompt.Value),
		Version:      "0.0.1",
	}, nil
}

func createExtensionDirectory(ctx context.Context, azdClient *azdext.AzdClient, extensionMetadata *models.ExtensionSchema) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}

	extensionPath := filepath.Join(cwd, extensionMetadata.Id)

	info, err := os.Stat(extensionPath)
	if err == nil && info.IsDir() {
		azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
			Options: &azdext.ConfirmOptions{
				Message:      fmt.Sprintf("The extension directory '%s' already exists. Do you want to continue?", extensionMetadata.Id),
				DefaultValue: internal.ToPtr(false),
			},
		})
	}

	if os.IsNotExist(err) {
		// Create the extension directory
		if err := os.MkdirAll(extensionPath, internal.PermissionDirectory); err != nil {
			return fmt.Errorf("failed to create extension directory: %w", err)
		}
	}

	// Create the extension.yaml file
	yamlBytes, err := yaml.Marshal(extensionMetadata)
	if err != nil {
		return fmt.Errorf("failed to marshal extension metadata to YAML: %w", err)
	}

	extensionFilePath := filepath.Join(extensionPath, "extension.yaml")
	if err := os.WriteFile(extensionFilePath, yamlBytes, internal.PermissionFile); err != nil {
		return fmt.Errorf("failed to create extension.yaml file: %w", err)
	}

	return nil
}
