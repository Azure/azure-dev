// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulator

import (

	// Importing for infrastructure provider plugin registrations

	"github.com/spf13/cobra"
)

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "az",
		Short: "Emulation mode for Azure CLI",
	}
	rootCmd.AddCommand(versionCmd())
	rootCmd.AddCommand(accountCommands())
	return rootCmd
}
