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
	// Version is populated at build time.
	Version = "dev"
	// Commit is populated at build time.
	Commit = "none"
	// BuildDate is populated at build time.
	BuildDate = "unknown"
)

func newVersionCommand(outputFormat *string) *cobra.Command {
	return azdext.RegisterFlagOptions(&cobra.Command{
		Use:   "version",
		Short: "Display the extension version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if outputFormat != nil && *outputFormat == "json" {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{
					"name":    "azure.ai.latency",
					"version": Version,
				})
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "azure.ai.latency %s\n", Version)
			return err
		},
	}, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default", "json"},
	})
}
