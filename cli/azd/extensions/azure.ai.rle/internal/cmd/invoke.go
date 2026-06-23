// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newInvokeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "invoke",
		Short: "Trigger an RLE training job (placeholder)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return &azdext.LocalError{
				Message:    "azd ai rle invoke is not implemented yet.",
				Code:       "rle_invoke_not_implemented",
				Category:   azdext.LocalErrorCategoryCompatibility,
				Suggestion: "Use this command later to trigger the training job for the deployed RLE environment.",
			}
		},
	}
}
