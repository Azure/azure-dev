// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"azure.ai.models/internal/client"
	"azure.ai.models/internal/utils"
	"azure.ai.models/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type customUpdateFlags struct {
	Name        string
	Version     string
	Description string
	SetTags     []string
	RemoveTags  []string
	Output      string
}

func newCustomUpdateCommand(parentFlags *customFlags) *cobra.Command {
	flags := &customUpdateFlags{}

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update a custom model",
		Long: `Update the description or tags of a custom model version.

Uses JSON Merge Patch (RFC 7396) semantics:
  - Tags are merged per-key: --set-tag adds or updates, --remove-tag deletes
  - Absent keys are left unchanged`,
		Example: `  # Update description
  azd ai models update --name my-model --version 1 --description "Updated description"

  # Add/update tags
  azd ai models update --name my-model --set-tag team=medical-ai --set-tag env=prod

  # Remove a tag
  azd ai models update --name my-model --remove-tag old-tag

  # Combine description and tag changes
  azd ai models update --name my-model --description "New desc" --set-tag v=2 --remove-tag draft`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			// Validate at least one update field is provided
			if !cmd.Flags().Changed("description") && len(flags.SetTags) == 0 && len(flags.RemoveTags) == 0 {
				return fmt.Errorf(
					"at least one of --description, --set-tag, or --remove-tag is required")
			}

			return runCustomUpdate(ctx, parentFlags, flags, cmd.Flags().Changed("description"))
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Model name (required)")
	cmd.Flags().StringVar(&flags.Version, "version", "1", "Model version")
	cmd.Flags().StringVar(&flags.Description, "description", "", "New model description")
	cmd.Flags().StringArrayVar(&flags.SetTags, "set-tag", nil,
		"Set a tag (key=value); can be specified multiple times")
	cmd.Flags().StringArrayVar(&flags.RemoveTags, "remove-tag", nil,
		"Remove a tag by key; can be specified multiple times")
	cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format (table, json)")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func runCustomUpdate(
	ctx context.Context,
	parentFlags *customFlags,
	flags *customUpdateFlags,
	descriptionChanged bool,
) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("failed waiting for debugger: %w", err)
	}

	// Build the merge-patch request
	updateReq := &models.UpdateModelRequest{}

	if descriptionChanged {
		updateReq.Description = &flags.Description
	}

	if len(flags.SetTags) > 0 || len(flags.RemoveTags) > 0 {
		updateReq.Tags = make(map[string]*string)

		for _, tag := range flags.SetTags {
			key, value, ok := strings.Cut(tag, "=")
			if !ok || key == "" {
				return fmt.Errorf("invalid tag format %q: expected key=value", tag)
			}
			updateReq.Tags[key] = &value
		}

		for _, key := range flags.RemoveTags {
			if key == "" {
				return fmt.Errorf("--remove-tag value cannot be empty")
			}
			updateReq.Tags[key] = nil // null removes the tag per RFC 7396
		}
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Updating model...",
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("failed to start spinner: %v\n", err)
	}

	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		_ = spinner.Stop(ctx)
		fmt.Println()
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	foundryClient, err := client.NewFoundryClient(parentFlags.projectEndpoint, credential)
	if err != nil {
		_ = spinner.Stop(ctx)
		fmt.Println()
		return err
	}

	model, err := foundryClient.UpdateModel(ctx, flags.Name, flags.Version, updateReq)
	_ = spinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		return fmt.Errorf("failed to update model: %w", err)
	}

	color.Green("✓ Model '%s' (version %s) updated", flags.Name, flags.Version)
	fmt.Println()

	switch flags.Output {
	case "json":
		if err := utils.PrintObject(model, utils.FormatJSON); err != nil {
			return err
		}
	case "table", "":
		fmt.Println(strings.Repeat("─", 50))
		fmt.Printf("  Name:        %s\n", model.Name)
		fmt.Printf("  Version:     %s\n", model.Version)
		if model.Description != "" {
			fmt.Printf("  Description: %s\n", model.Description)
		}
		if len(model.Tags) > 0 {
			fmt.Println("  Tags:")
			for k, v := range model.Tags {
				fmt.Printf("    %s: %s\n", k, v)
			}
		}
		fmt.Println(strings.Repeat("─", 50))
	default:
		return fmt.Errorf("unsupported output format: %s (supported: table, json)", flags.Output)
	}

	return nil
}
