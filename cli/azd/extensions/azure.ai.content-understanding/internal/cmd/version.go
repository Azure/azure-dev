// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

var (
	// Populated at build time
	Version   = "dev" // Default value for development builds
	Commit    = "none"
	BuildDate = "unknown"
)

func newVersionCommand(outputFormat *string) *cobra.Command {
	return azdext.RegisterFlagOptions(&cobra.Command{
		Use:   "version",
		Short: "Display the extension version",
		Args:  cobra.NoArgs,
		Example: `  azd ai cu version
    Show the installed extension version.

  azd ai cu version --output json
    Return the extension ID and version as JSON.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if *outputFormat == "json" {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
					"name": extensionID, "version": Version,
				}); err != nil {
					return fmt.Errorf("write extension version: %w", err)
				}
				return nil
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", extensionID, Version)
			return err
		},
	}, azdext.FlagOptions{Name: "output", AllowedValues: []string{"json"}})
}
