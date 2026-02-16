// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"azure.ai.models/internal/azcopy"
	"azure.ai.models/internal/client"
	"azure.ai.models/pkg/models"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/ux"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

type customCreateFlags struct {
	Name        string
	Version     string
	Source      string
	SourceFile  string
	Description string
	BaseModel   string
	AzcopyPath  string
}

func newCustomCreateCommand(parentFlags *customFlags) *cobra.Command {
	flags := &customCreateFlags{}

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Upload and register a custom model",
		Long: `Upload model weights to Azure AI Foundry and register the model.

This command performs three steps:
  1. Requests a writable upload location (SAS URI)
  2. Uploads model files using AzCopy
  3. Registers the model in the custom model registry

The --source flag accepts a local file/directory path or a remote blob URL with SAS token.
For remote URLs containing special characters (& in SAS tokens), use --source-file to
provide a file containing the URL instead.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			// Resolve source from --source-file if --source is not set
			if flags.Source == "" && flags.SourceFile != "" {
				data, err := os.ReadFile(flags.SourceFile)
				if err != nil {
					return fmt.Errorf("failed to read source file '%s': %w", flags.SourceFile, err)
				}
				flags.Source = strings.TrimSpace(string(data))
				if flags.Source == "" {
					return fmt.Errorf("source file '%s' is empty", flags.SourceFile)
				}
			}

			if flags.Source == "" {
				return fmt.Errorf("either --source or --source-file is required")
			}

			return runCustomCreate(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Model name (required)")
	cmd.Flags().StringVar(&flags.Source, "source", "", "Local path or remote URL to model files")
	cmd.Flags().StringVar(&flags.SourceFile, "source-file", "", "Path to a file containing the source URL (useful for URLs with special characters)")
	cmd.Flags().StringVar(&flags.Version, "version", "1", "Model version")
	cmd.Flags().StringVar(&flags.Description, "description", "", "Model description")
	cmd.Flags().StringVar(&flags.BaseModel, "base-model", "", "Base model architecture (e.g., FW-DeepSeek-v3.1)")
	cmd.Flags().StringVar(&flags.AzcopyPath, "azcopy-path", "", "Path to azcopy binary (auto-detected if not provided)")

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

	// Step 0: Verify azcopy is available before making any API calls
	azRunner, err := azcopy.NewRunner(flags.AzcopyPath)
	if err != nil {
		return err
	}
	fmt.Printf("  Using azcopy: %s\n\n", azRunner.Path())

	// Create Azure credential
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	foundryClient, err := client.NewFoundryClient(parentFlags.projectEndpoint, credential)
	if err != nil {
		return err
	}

	// ── Step 1: Start pending upload ──
	fmt.Printf("Creating custom model: %s (version %s)\n\n", flags.Name, flags.Version)

	spinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Step 1/3: Requesting upload location...",
	})
	if err := spinner.Start(ctx); err != nil {
		fmt.Printf("failed to start spinner: %v\n", err)
	}

	uploadResp, err := foundryClient.StartPendingUpload(ctx, flags.Name, flags.Version)
	_ = spinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		return fmt.Errorf("failed to get upload location: %w", err)
	}

	if uploadResp.BlobReferenceForConsumption == nil {
		return fmt.Errorf("unexpected response: no blob reference returned")
	}

	blob := uploadResp.BlobReferenceForConsumption
	color.Green("✓ Upload location ready")
	fmt.Printf("  Blob URI: %s\n\n", blob.BlobURI)

	// ── Step 2: Upload via AzCopy ──
	fmt.Println("Step 2/3: Uploading model files...")
	fmt.Printf("  Source: %s\n", flags.Source)
	fmt.Println()

	if err := azRunner.Copy(ctx, flags.Source, blob.Credential.SasURI); err != nil {
		// Upload failed — print manual recovery instructions
		fmt.Println()
		color.Red("✗ Upload failed: %v", err)
		fmt.Println()
		color.Yellow("You can retry the upload manually:")
		fmt.Println()
		sourceHint := fmt.Sprintf("%s/*", flags.Source)
		if strings.HasPrefix(flags.Source, "https://") || strings.HasPrefix(flags.Source, "http://") {
			sourceHint = flags.Source
		}
		fmt.Printf("  azcopy copy \"%s\" \"%s\" --recursive=true\n", sourceHint, blob.Credential.SasURI)
		fmt.Println()
		color.Yellow("After upload completes, register the model with:")
		fmt.Println()
		fmt.Printf("  azd ai models custom register --name %s --version %s --blob-uri \"%s\" -e \"%s\"\n",
			flags.Name, flags.Version, blob.BlobURI, parentFlags.projectEndpoint)
		return fmt.Errorf("upload failed")
	}

	fmt.Println()
	color.Green("✓ Upload complete")
	fmt.Println()

	// ── Step 3: Register model ──
	regSpinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Step 3/3: Registering model...",
	})
	if err := regSpinner.Start(ctx); err != nil {
		fmt.Printf("failed to start spinner: %v\n", err)
	}

	regReq := &models.RegisterModelRequest{
		BlobURI:     blob.BlobURI,
		Description: flags.Description,
	}

	if flags.BaseModel != "" {
		regReq.Tags = map[string]string{
			"baseArchitecture": flags.BaseModel,
		}
	}

	model, err := foundryClient.RegisterModel(ctx, flags.Name, flags.Version, regReq)
	_ = regSpinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		// Upload succeeded but register failed — print recovery instructions
		color.Red("✗ Registration failed: %v", err)
		fmt.Println()
		color.Yellow("Upload completed successfully. You can register the model manually:")
		fmt.Println()
		fmt.Printf("  azd ai models custom register --name %s --version %s --blob-uri \"%s\" -e \"%s\"\n",
			flags.Name, flags.Version, blob.BlobURI, parentFlags.projectEndpoint)
		return fmt.Errorf("registration failed")
	}

	// ── Success ──
	color.Green("✓ Model registered successfully!")
	fmt.Println()
	fmt.Println(strings.Repeat("─", 50))
	fmt.Printf("  Name:        %s\n", model.Name)
	fmt.Printf("  Version:     %s\n", model.Version)
	if model.Description != "" {
		fmt.Printf("  Description: %s\n", model.Description)
	}
	if model.SystemData != nil && model.SystemData.CreatedAt != "" {
		fmt.Printf("  Created:     %s\n", model.SystemData.CreatedAt)
	}
	fmt.Println(strings.Repeat("─", 50))

	return nil
}
