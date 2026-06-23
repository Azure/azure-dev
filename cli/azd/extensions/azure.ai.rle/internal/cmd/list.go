// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newListCommand() *cobra.Command {
	flags := &sharedFlags{
		account: defaultAccountName,
		project: defaultProjectName,
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List RLE environments",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			environments, err := client.listEnvironments(cmd.Context(), flags.account, flags.project)
			if err != nil {
				return serviceError(err)
			}

			encoded, err := json.MarshalIndent(environments, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
			return err
		},
	}

	addSharedFlags(cmd, flags)
	return cmd
}
