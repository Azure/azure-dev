// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"strings"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// connectionRemoveFlags carries the verb-specific flags for `connection remove`.
type connectionRemoveFlags struct {
	force bool
}

// newToolboxConnectionRemoveCommand returns the `connection remove` command.
func newToolboxConnectionRemoveCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &connectionRemoveFlags{}

	cmd := &cobra.Command{
		Use:   "remove <toolbox> <connection>",
		Short: "Detach a project connection from a toolbox.",
		Long: `Detach a project connection from a toolbox.

Publishes a new default version with the named connection's tool entry
removed. Refuses to leave the toolbox with zero tools (use 'toolbox delete'
instead).`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnectionRemove(
				cmd.Context(), args[0], args[1],
				*flags,
				readToolboxFlags(cmd, extCtx),
				defaultConnectionResolver{},
			)
		},
	}
	cmd.Flags().BoolVar(
		&flags.force, "force", false,
		"Skip confirmation prompts and apply the removal immediately.",
	)
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runConnectionRemove(
	ctx context.Context, toolboxName, connName string,
	verb connectionRemoveFlags,
	parent toolboxFlags, resolver connectionResolver,
) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	if strings.TrimSpace(connName) == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidPositionalArg,
			"<connection> must not be empty",
			"pass the short name of a project connection",
		)
	}
	if parent.noPrompt && !verb.force {
		return exterrors.Validation(
			exterrors.CodeMissingForceFlag,
			"--no-prompt requires --force for connection removal",
			"add --force to confirm the operation non-interactively",
		)
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox connection remove", resolved)

	return runConnectionRemoveWith(ctx, client, resolver, resolved.Endpoint,
		toolboxName, connName, verb, parent)
}

func runConnectionRemoveWith(
	ctx context.Context, client toolboxClient, resolver connectionResolver,
	endpoint, toolboxName, connName string,
	verb connectionRemoveFlags,
	parent toolboxFlags,
) error {
	conn, err := resolver.resolveConnection(ctx, endpoint, connName)
	if err != nil {
		return err
	}

	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		return toolboxNotFoundOrService(err, toolboxName, exterrors.OpGetToolbox)
	}

	current, err := client.GetToolboxVersion(ctx, toolboxName, tb.DefaultVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	filtered, removed := filterOutConnection(current.Tools, conn.ID)
	if !removed {
		return exterrors.Validation(
			exterrors.CodeConnectionNotInToolbox,
			fmt.Sprintf(
				"connection %q is not attached to toolbox %q's current default version",
				connName, toolboxName,
			),
			fmt.Sprintf("run 'azd ai toolbox connection list %q'", toolboxName),
		)
	}
	if len(filtered) == 0 {
		return exterrors.Validation(
			exterrors.CodeLastToolRemoval,
			fmt.Sprintf(
				"removing %q would leave toolbox %q with zero tools",
				connName, toolboxName,
			),
			fmt.Sprintf(
				"delete the toolbox with `azd ai toolbox delete %q` instead",
				toolboxName,
			),
		)
	}

	if !verb.force {
		shouldProceed := true
		err := withAzdClient(func(azdClient *azdext.AzdClient) error {
			confirmed, err := confirmToolboxDelete(
				ctx,
				azdClient,
				fmt.Sprintf(
					"Detach connection %q from toolbox %q (publishes a new version)?",
					connName,
					toolboxName,
				),
			)
			if err != nil {
				return err
			}
			if !confirmed {
				shouldProceed = false
				fmt.Println("Aborted.")
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !shouldProceed {
			return nil
		}
	}

	req := &azure.CreateToolboxVersionRequest{
		Description: current.Description,
		Metadata:    current.Metadata,
		Tools:       filtered,
		Policies:    current.Policies,
	}
	created, err := client.CreateToolboxVersion(ctx, toolboxName, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
	}
	if _, err := client.SetDefaultVersion(ctx, toolboxName, created.Version); err != nil {
		return exterrors.Dependency(
			exterrors.CodeSetDefaultVersionFailed,
			fmt.Sprintf(
				"toolbox %q version %q was created but could not be promoted to default: %s",
				toolboxName, created.Version, err,
			),
			fmt.Sprintf(
				"run `azd ai toolbox update %q --default-version %q` to retarget the default",
				toolboxName, created.Version,
			),
		)
	}

	return emitConnectionRemoveResult(toolboxName, created.Version, conn, parent.output)
}

func emitConnectionRemoveResult(
	toolboxName, newVersion string, conn *projectConnection, output string,
) error {
	if output == "json" {
		payload := map[string]any{
			"toolbox":       toolboxName,
			"version":       newVersion,
			"connection":    conn.Name,
			"connection_id": conn.ID,
		}
		return emitJSON(payload)
	}
	fmt.Printf(
		"Detached connection %s from toolbox %s (now at version %s).\n",
		conn.Name, toolboxName, newVersion,
	)
	return nil
}
