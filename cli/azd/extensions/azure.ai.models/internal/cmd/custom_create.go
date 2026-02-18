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
	BlobURI     string
	Description string
	BaseModel   string
	Publisher   string
	AzcopyPath  string
	// NoWait   bool // TODO: re-enable with async registration when data-plane polling is available
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
provide a file containing the URL instead.

If you have already uploaded model files, use --blob-uri to skip upload and register directly.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			// Validate mutually exclusive flags
			if flags.BlobURI != "" && (flags.Source != "" || flags.SourceFile != "") {
				return fmt.Errorf("--blob-uri cannot be used with --source or --source-file")
			}

			if flags.BlobURI != "" {
				if !strings.HasPrefix(flags.BlobURI, "https://") {
					return fmt.Errorf("--blob-uri must be an HTTPS URL")
				}
				return runCustomCreateFromBlobURI(ctx, parentFlags, flags)
			}

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
				return fmt.Errorf("either --source, --source-file, or --blob-uri is required")
			}

			return runCustomCreate(ctx, parentFlags, flags)
		},
	}

	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "Model name (required)")
	cmd.Flags().StringVar(&flags.Source, "source", "", "Local path or remote URL to model files")
	cmd.Flags().StringVar(&flags.SourceFile, "source-file", "", "Path to a file containing the source URL (useful for URLs with special characters)")
	cmd.Flags().StringVar(&flags.BlobURI, "blob-uri", "", "Already-uploaded blob URI (skips upload, registers directly)")
	cmd.Flags().StringVar(&flags.Version, "version", "1", "Model version")
	cmd.Flags().StringVar(&flags.Description, "description", "", "Model description")
	cmd.Flags().StringVar(&flags.BaseModel, "base-model", "", "Base model identifier (e.g., FW-GPT-OSS-120B or full azureml:// URI)")
	cmd.Flags().StringVar(&flags.Publisher, "publisher", "Fireworks", "Model publisher ID for catalog info")
	cmd.Flags().StringVar(&flags.AzcopyPath, "azcopy-path", "", "Path to azcopy binary (auto-detected if not provided)")
	// TODO: re-enable --no-wait when data-plane polling endpoint is available
	// cmd.Flags().BoolVar(&flags.NoWait, "no-wait", false, "Start async registration and return immediately with the operation URL")

	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("base-model")

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
	azRunner, err := azcopy.NewRunner(ctx, flags.AzcopyPath)
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
		sourceHint := flags.Source
		if !strings.HasPrefix(flags.Source, "https://") && !strings.HasPrefix(flags.Source, "http://") {
			if info, statErr := os.Stat(flags.Source); statErr == nil && info.IsDir() {
				sourceHint = fmt.Sprintf("%s/*", flags.Source)
			}
		}
		fmt.Printf("  azcopy copy \"%s\" \"%s\" --recursive=true\n", sourceHint, blob.Credential.SasURI)
		fmt.Println()
		color.Yellow("After upload completes, re-run with --blob-uri to register the model:")
		fmt.Println()
		fmt.Printf("  azd ai models custom create --name %s --version %s --blob-uri \"%s\" -e \"%s\"\n",
			flags.Name, flags.Version, blob.BlobURI, parentFlags.projectEndpoint)
		return fmt.Errorf("upload failed: %w", err)
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

	derivedURI := buildDerivedModelURI(flags.BaseModel)
	regReq := &models.RegisterModelRequest{
		BlobURI:     blob.BlobURI,
		Description: flags.Description,
		CatalogInfo: &models.CatalogInfo{
			PublisherID: flags.Publisher,
		},
		DerivedModelInformation: &models.DerivedModelInformation{
			BaseModel: &derivedURI,
		},
	}

	regReq.Tags = map[string]string{
		"baseArchitecture": extractBaseModelName(flags.BaseModel),
	}

	model, err := foundryClient.RegisterModel(ctx, flags.Name, flags.Version, regReq)
	_ = regSpinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		// Upload succeeded but register failed — print recovery instructions
		color.Red("✗ Registration failed: %v", err)
		fmt.Println()
		color.Yellow("Upload completed successfully. You can retry registration with --blob-uri:")
		fmt.Println()
		fmt.Printf("  azd ai models custom create --name %s --version %s --blob-uri \"%s\" -e \"%s\"\n",
			flags.Name, flags.Version, blob.BlobURI, parentFlags.projectEndpoint)
		return fmt.Errorf("registration failed: %w", err)
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

// runCustomCreateFromBlobURI registers a model directly from an already-uploaded blob URI,
// skipping the upload steps.
func runCustomCreateFromBlobURI(ctx context.Context, parentFlags *customFlags, flags *customCreateFlags) error {
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

	fmt.Printf("Creating custom model: %s (version %s)\n\n", flags.Name, flags.Version)
	fmt.Printf("  Using provided blob URI, skipping upload...\n")
	fmt.Printf("  Blob URI: %s\n\n", flags.BlobURI)

	// ── Register model ──
	regSpinner := ux.NewSpinner(&ux.SpinnerOptions{
		Text: "Registering model...",
	})
	if err := regSpinner.Start(ctx); err != nil {
		fmt.Printf("failed to start spinner: %v\n", err)
	}

	derivedURI := buildDerivedModelURI(flags.BaseModel)
	regReq := &models.RegisterModelRequest{
		BlobURI:     flags.BlobURI,
		Description: flags.Description,
		CatalogInfo: &models.CatalogInfo{
			PublisherID: flags.Publisher,
		},
		DerivedModelInformation: &models.DerivedModelInformation{
			BaseModel: &derivedURI,
		},
	}

	regReq.Tags = map[string]string{
		"baseArchitecture": extractBaseModelName(flags.BaseModel),
	}

	model, err := foundryClient.RegisterModel(ctx, flags.Name, flags.Version, regReq)
	_ = regSpinner.Stop(ctx)
	fmt.Println()

	if err != nil {
		color.Red("✗ Registration failed: %v", err)
		fmt.Println()
		color.Yellow("You can retry registration by re-running:")
		fmt.Println()
		fmt.Printf("  azd ai models custom create --name %s --version %s --blob-uri \"%s\" -e \"%s\"\n",
			flags.Name, flags.Version, flags.BlobURI, parentFlags.projectEndpoint)
		return fmt.Errorf("registration failed: %w", err)
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

// buildDerivedModelURI returns the base-model value as a full azureml:// URI.
// If the value is already a full URI, it is returned as-is.
// Otherwise it is treated as a model name and wrapped in the default registry path.
func buildDerivedModelURI(baseModel string) string {
	if strings.HasPrefix(baseModel, "azureml://") {
		return baseModel
	}
	return fmt.Sprintf("azureml://registries/azureml-fireworks/models/%s/versions/1", baseModel)
}

// extractBaseModelName extracts the model name from a base-model value.
// If the value is an azureml:// URI (e.g. azureml://registries/azureml-fireworks/models/FW-GPT-OSS-120B/versions/1),
// extracts and returns the model name segment ("FW-GPT-OSS-120B").
// Otherwise returns the value as-is.
func extractBaseModelName(baseModel string) string {
	if !strings.HasPrefix(baseModel, "azureml://") {
		return baseModel
	}
	// URI format: azureml://registries/{reg}/models/{name}/versions/{ver}
	parts := strings.Split(baseModel, "/")
	for i, p := range parts {
		if p == "models" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return baseModel
}
