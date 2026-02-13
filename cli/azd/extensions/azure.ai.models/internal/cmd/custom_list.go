// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"

	"azure.ai.models/internal/client"
	"azure.ai.models/internal/utils"
	"azure.ai.models/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/spf13/cobra"
)

type customListFlags struct {
	Output string
}

func newCustomListCommand(parentFlags *customFlags) *cobra.Command {
	flags := &customListFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all custom models",
		Long:  "List all custom models registered in the Azure AI Foundry custom model registry.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runCustomList(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format (table, json)")

	return cmd
}

func runCustomList(ctx context.Context, parentFlags *customFlags, flags *customListFlags) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("failed to create azd client: %w", err)
	}
	defer azdClient.Close()

	// Wait for debugger if AZD_EXT_DEBUG is set
	if err := azdext.WaitForDebugger(ctx, azdClient); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, azdext.ErrDebuggerAborted) {
			return nil
		}
		return fmt.Errorf("failed waiting for debugger: %w", err)
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Fetching custom models...",
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

	result, err := foundryClient.ListModels(ctx)
	_ = spinner.Stop(ctx)
	fmt.Print("\n\n")

	if err != nil {
		return err
	}

	switch flags.Output {
	case "json":
		utils.PrintObject(result.Value, utils.FormatJSON)
	case "table", "":
		views := make([]models.CustomModelListView, len(result.Value))
		for i, m := range result.Value {
			views[i] = m.ToListView()
		}
		utils.PrintObject(views, utils.FormatTable)
	default:
		return fmt.Errorf("unsupported output format: %s (supported: table, json)", flags.Output)
	}

	fmt.Printf("\n%d custom model(s) found\n", len(result.Value))
	return nil
}
