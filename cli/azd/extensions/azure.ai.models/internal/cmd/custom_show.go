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

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type customShowFlags struct {
	Name    string
	Version string
	Output  string
}

func newCustomShowCommand(parentFlags *customFlags) *cobra.Command {
	flags := &customShowFlags{}

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show details of a custom model",
		Long:  "Show detailed information about a specific custom model in the Azure AI Foundry custom model registry.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runCustomShow(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Model name (required)")
	cmd.Flags().StringVar(&flags.Version, "version", "1", "Model version")
	cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format (table, json)")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func runCustomShow(ctx context.Context, parentFlags *customFlags, flags *customShowFlags) error {
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

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Fetching model details...",
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

	model, err := foundryClient.GetModel(ctx, flags.Name, flags.Version)
	_ = spinner.Stop(ctx)
	fmt.Print("\n\n")

	if err != nil {
		return err
	}

	switch flags.Output {
	case "json":
		if err := utils.PrintObject(model, utils.FormatJSON); err != nil {
			return err
		}
	case "table", "":
		fmt.Printf("Custom Model: %s\n", model.Name)
		fmt.Println(strings.Repeat("─", 50))

		fmt.Println("\nGeneral:")
		fmt.Printf("  Name:         %s\n", model.Name)
		fmt.Printf("  Version:      %s\n", model.Version)
		if model.DisplayName != "" {
			fmt.Printf("  Display Name: %s\n", model.DisplayName)
		}
		if model.Description != "" {
			fmt.Printf("  Description:  %s\n", model.Description)
		}

		if model.SystemData != nil {
			fmt.Println("\nSystem Data:")
			if model.SystemData.CreatedAt != "" {
				fmt.Printf("  Created:       %s\n", model.SystemData.CreatedAt)
			}
			if model.SystemData.CreatedBy != "" {
				fmt.Printf("  Created By:    %s\n", model.SystemData.CreatedBy)
			}
			if model.SystemData.LastModifiedAt != "" {
				fmt.Printf("  Last Modified: %s\n", model.SystemData.LastModifiedAt)
			}
		}

		if model.BlobURI != "" {
			fmt.Println("\nStorage:")
			fmt.Printf("  Blob URI: %s\n", model.BlobURI)
		}

		if model.DerivedModelInformation != nil && model.DerivedModelInformation.BaseModel != nil {
			fmt.Println("\nDerived Model:")
			fmt.Printf("  Base Model: %s\n", *model.DerivedModelInformation.BaseModel)
		}

		if len(model.Tags) > 0 {
			fmt.Println("\nTags:")
			for k, v := range model.Tags {
				fmt.Printf("  %s: %s\n", k, v)
			}
		}

		fmt.Println(strings.Repeat("─", 50))
	default:
		return fmt.Errorf("unsupported output format: %s (supported: table, json)", flags.Output)
	}

	return nil
}
