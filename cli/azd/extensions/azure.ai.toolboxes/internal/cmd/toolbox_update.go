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

// toolboxUpdateFlags carries the verb-specific flags for `toolbox update`.
type toolboxUpdateFlags struct {
	defaultVersion string
}

// newToolboxUpdateCommand returns the `azd ai toolbox update <name>` command.
// Only --default-version is supported.
func newToolboxUpdateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &toolboxUpdateFlags{}

	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update a toolbox (currently: retarget the default version).",
		Long: `Update a toolbox.

Only --default-version is supported today. To change the tool list, publish a
new version with 'connection add' or 'connection remove'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxUpdate(cmd.Context(), args[0], *flags, readToolboxFlags(cmd, extCtx))
		},
	}

	cmd.Flags().StringVar(
		&flags.defaultVersion, "default-version", "",
		"Version string to mark as the default for this toolbox.",
	)
	registerToolboxOutputFlag(cmd)

	return cmd
}

func runToolboxUpdate(
	ctx context.Context, name string, verb toolboxUpdateFlags, parent toolboxFlags,
) error {
	if err := validateToolboxName(name); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	if strings.TrimSpace(verb.defaultVersion) == "" {
		return exterrors.Validation(
			exterrors.CodeMissingUpdateField,
			"no fields to update",
			"specify --default-version",
		)
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox update", resolved)

	result, err := client.SetDefaultVersion(ctx, name, verb.defaultVersion)
	if err != nil {
		return toolboxNotFoundOrService(err, name, exterrors.OpSetDefaultVersion)
	}

	if parent.output == "json" {
		return emitJSON(result)
	}
	fmt.Printf("Toolbox %s default version set to %s.\n", name, result.DefaultVersion)
	return nil
}
