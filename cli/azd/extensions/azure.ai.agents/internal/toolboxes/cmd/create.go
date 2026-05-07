// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type ToolboxesCreateAction struct {
	flags *toolboxesCreateFlags
}

type toolboxesCreateFlags struct {
}

func newCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &toolboxesCreateFlags{}
	action := &ToolboxesCreateAction{flags: flags}
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new toolbox.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := azdext.WithAccessToken(cmd.Context())

			return action.Run(ctx)
		},
	}

	return cmd
}

func (a *ToolboxesCreateAction) Run(ctx context.Context) error {
	return fmt.Errorf("not implemented")
}
