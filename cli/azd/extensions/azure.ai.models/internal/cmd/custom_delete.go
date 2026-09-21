// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"azure.ai.models/internal/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type customDeleteFlags struct {
	Name    string
	Version string
	Force   bool
}

func newCustomDeleteCommand(parentFlags *customFlags) *cobra.Command {
	flags := &customDeleteFlags{}

	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a custom model",
		Long:  "Delete a custom model version from the Azure AI Foundry custom model registry.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runCustomDelete(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Model name (required)")
	cmd.Flags().StringVar(&flags.Version, "version", "1", "Model version")
	cmd.Flags().BoolVarP(&flags.Force, "force", "f", false, "Skip confirmation prompt")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func runCustomDelete(ctx context.Context, parentFlags *customFlags, flags *customDeleteFlags) error {
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

	// Confirmation prompt unless --force
	if !flags.Force && !rootFlags.NoPrompt {
		fmt.Printf("Delete custom model '%s' (version %s)? This action cannot be undone.\n", flags.Name, flags.Version)
		fmt.Print("Type the model name to confirm: ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input != flags.Name {
			fmt.Println("Deletion cancelled.")
			return nil
		}
	}

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Deleting custom model...",
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

	err = foundryClient.DeleteModel(ctx, flags.Name, flags.Version)
	_ = spinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		return err
	}

	color.Green("✓ Model '%s' (version %s) deleted", flags.Name, flags.Version)
	return nil
}
