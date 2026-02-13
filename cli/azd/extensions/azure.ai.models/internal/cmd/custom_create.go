// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"

	"azure.ai.models/internal/client"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type customCreateFlags struct {
	Name    string
	Version string
}

func newCustomCreateCommand(parentFlags *customFlags) *cobra.Command {
	flags := &customCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a custom model (start pending upload)",
		Long: `Initiate a custom model upload by requesting a writable blob storage location.

This command registers a pending model version and returns a SAS URI.
Use AzCopy to upload your model weights to the returned URI, then register the model.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			return runCustomCreate(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Model name (required)")
	cmd.Flags().StringVar(&flags.Version, "version", "1", "Model version")

	_ = cmd.MarkFlagRequired("name")

	return cmd
}

func runCustomCreate(ctx context.Context, parentFlags *customFlags, flags *customCreateFlags) error {
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
		Text: "Preparing upload location...",
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

	result, err := foundryClient.StartPendingUpload(ctx, flags.Name, flags.Version)
	_ = spinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		return err
	}

	if result.BlobReferenceForConsumption == nil {
		return fmt.Errorf("unexpected response: no blob reference returned")
	}

	blob := result.BlobReferenceForConsumption

	color.Green("✓ Upload location ready\n")
	fmt.Println()
	fmt.Printf("  Model Name:  %s\n", flags.Name)
	fmt.Printf("  Version:     %s\n", flags.Version)
	fmt.Printf("  Blob URI:    %s\n", blob.BlobURI)
	fmt.Println()

	color.Cyan("Upload your model files using AzCopy:\n")
	fmt.Println()
	fmt.Printf("  azcopy copy \"<source-local-folder-path>/*\" \"%s\" --recursive=true\n", blob.Credential.SasURI)
	fmt.Println()

	color.Yellow("After upload completes, register the model with:\n")
	fmt.Println()
	fmt.Printf("  azd ai models custom register --name %s --version %s --blob-uri \"%s\"\n",
		flags.Name, flags.Version, blob.BlobURI)
	fmt.Println()

	return nil
}
