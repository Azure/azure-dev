// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newCreateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new RLE resource",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return notImplementedError("create", args[0])
		},
	}
}

func notImplementedError(commandName string, resourceName string) error {
	return &azdext.LocalError{
		Message:    fmt.Sprintf("azd ai rle %s is not implemented yet for %q.", commandName, resourceName),
		Code:       fmt.Sprintf("%s_not_implemented", commandName),
		Category:   azdext.LocalErrorCategoryCompatibility,
		Suggestion: "Add the RLE service workflow for this command, then try again.",
	}
}
