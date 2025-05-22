// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"

	"github.com/azure/azure-dev/cli/azd/extensions/wallace.scripts/internal/providers/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func newListenCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "listen",
		Short: "Starts the extension and listens for events.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Create a new context that includes the AZD access token.
			ctx := azdext.WithAccessToken(cmd.Context())

			// Create a new AZD client.
			azdClient, err := azdext.NewAzdClient()
			if err != nil {
				return fmt.Errorf("failed to create azd client: %w", err)
			}
			defer azdClient.Close()

			provider := provisioning.NewScriptProvider(azdClient)
			provisioningManager := azdext.NewProvisioningManager(azdClient)
			if err := provisioningManager.Register(ctx, provider, "scripts", "Custom Scripts"); err != nil {
				return fmt.Errorf("failed to register provider: %w", err)
			}

			if _, err := azdClient.Extension().Ready(ctx, &azdext.ReadyRequest{}); err != nil {
				return fmt.Errorf("failed to signal readiness: %w", err)
			}

			select {}
		},
	}
}
