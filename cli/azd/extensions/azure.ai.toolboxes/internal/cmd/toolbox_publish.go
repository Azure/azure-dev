// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxPublishCommand returns the `azd ai toolbox publish <name> <version>` command.
func newToolboxPublishCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "publish <name> <version>",
		Short: "Set the default version for a toolbox.",
		Long: `Set the default version for a toolbox.

This promotes a previously created version so that consumers referencing the
toolbox without an explicit version will receive it. To create a new version,
use 'connection add', 'connection remove', 'skill add', or 'skill remove'.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxPublish(cmd.Context(), args[0], args[1], readToolboxFlags(cmd, extCtx))
		},
	}

	registerToolboxOutputFlag(cmd)

	return cmd
}

func runToolboxPublish(
	ctx context.Context, name string, version string, parent toolboxFlags,
) error {
	if err := validateToolboxName(name); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	if strings.TrimSpace(version) == "" {
		return exterrors.Validation(
			exterrors.CodeMissingUpdateField,
			"version must not be empty",
			"pass the version to promote as the second positional argument",
		)
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox publish", resolved)

	result, err := client.SetDefaultVersion(ctx, name, version)
	if err != nil {
		return toolboxNotFoundOrService(err, name, exterrors.OpSetDefaultVersion)
	}

	if parent.output == "json" {
		return emitJSON(result)
	}
	fmt.Printf("Toolbox %s default version set to %s.\n", name, result.DefaultVersion)
	return nil
}
