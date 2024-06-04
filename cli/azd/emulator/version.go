// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package emulator

import (

	// Importing for infrastructure provider plugin registrations

	"fmt"

	"github.com/spf13/cobra"
)

const (
	emulatedAzVersion = `{"azure-cli": "2.61.0","azure-cli-core": "2.61.0","azure-cli-telemetry": "1.1.0","extensions": {}}`
)

func versionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(emulatedAzVersion)
		},
	}
	cmd.Flags().StringP("output", "o", "", "Output format")
	return cmd
}
