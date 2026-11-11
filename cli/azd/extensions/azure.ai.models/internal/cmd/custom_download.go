// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"strings"

	"azure.ai.models/internal/fireworks"
	"azure.ai.models/internal/utils"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type customDownloadFlags struct {
	Connection string
	Account    string
	Model      string
	OutputDir  string
	Output     string
}

func newCustomDownloadCommand(parentFlags *customFlags) *cobra.Command {
	flags := &customDownloadFlags{}
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download connected Fireworks model files to a new local directory",
		Long: "Download all exported files using a saved Foundry project connection to Fireworks. " +
			"Requires Fireworks model download access and API-key export support. " +
			"The output directory must not exist and its parent must exist. " +
			"Completed files remain if a later file fails; retry with a new directory. " +
			"This command does not import, register, or deploy a Foundry model.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.NoArgs(cmd, args); err != nil {
				return err
			}
			return validateCustomDownloadFlags(flags)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())
			provider, err := newFireworksClient(ctx, parentFlags.projectEndpoint, flags.Connection)
			if err != nil {
				return err
			}
			result, err := provider.DownloadModel(ctx, flags.Account, flags.Model, flags.OutputDir)
			if err != nil {
				return err
			}
			return utils.PrintObjectTo(cmd.OutOrStdout(), result, utils.OutputFormat(flags.Output))
		},
	}
	cmd.Flags().StringVar(&flags.Connection, "connection", "", "Saved Foundry project connection to Fireworks")
	cmd.Flags().StringVar(&flags.Account, "account", "", "Fireworks account ID (not an accounts/... resource path)")
	cmd.Flags().StringVar(&flags.Model, "model", "", "Fireworks model ID (not a model resource path)")
	cmd.Flags().StringVar(&flags.OutputDir, "output-dir", "", "New local directory for all exported model files")
	cmd.Flags().StringVarP(&flags.Output, "output", "o", "table", "Output format for the download summary (table, json)")
	return cmd
}

func validateCustomDownloadFlags(flags *customDownloadFlags) error {
	if strings.TrimSpace(flags.Connection) == "" {
		return fmt.Errorf("--connection must be non-empty")
	}
	if err := fireworks.ValidateAccount(flags.Account); err != nil {
		return err
	}
	if err := fireworks.ValidateModel(flags.Model); err != nil {
		return err
	}
	if flags.Output != "table" && flags.Output != "json" {
		return fmt.Errorf("unsupported output format: %s (supported: table, json)", flags.Output)
	}
	return fireworks.ValidateDownloadDirectory(flags.OutputDir)
}
