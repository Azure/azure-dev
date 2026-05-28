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

// newToolboxPublishCommand returns the `azd ai toolbox publish <name> <version>`
// command. This is the only verb that mutates the toolbox's default_version
// pointer; all other mutation verbs publish new immutable versions but leave
// the default alone.
func newToolboxPublishCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "publish <name> <version>",
		Short: "Promote a toolbox version to be the default.",
		Long: `Promote a published version of a toolbox to be its default.

Agents and other consumers that reference the toolbox by name resolve to the
default version. 'connection add', 'connection remove', 'skill add', and
'skill remove' publish new versions but never change the default; use this
verb when you're ready to make a version live.

Examples:

  azd ai toolbox publish research 3
`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolboxPublish(cmd.Context(), args[0], args[1], readToolboxFlags(cmd, extCtx))
		},
	}
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runToolboxPublish(
	ctx context.Context, name, version string, parent toolboxFlags,
) error {
	if err := validateToolboxName(name); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	if strings.TrimSpace(version) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidPositionalArg,
			"<version> must not be empty",
			"pass the version identifier to publish",
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
