// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "github.com/spf13/cobra"

func newModifyCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "modify <name>",
		Short: "Modify an existing RLE resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplementedError("modify", args[0])
		},
	}
}
