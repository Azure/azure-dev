// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionsCommand() *cobra.Command {
	flags := &sharedFlags{
		account: defaultAccountName,
		project: defaultProjectName,
	}

	cmd := &cobra.Command{
		Use:   "versions <environment-id>",
		Short: "List RLE environment versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client := newRleClient(resolveControlPlaneEndpoint(flags.endpoint))
			versions, err := client.listEnvironmentVersions(cmd.Context(), flags.account, flags.project, args[0])
			if err != nil {
				return serviceError(err)
			}

			encoded, err := json.MarshalIndent(versions, "", "  ")
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
