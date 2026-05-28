// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"
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
		Use:   "remove <toolbox> <connection>...",
		Short: "Detach one or more connections from a toolbox.",
		Long: `Detach one or more connections from a toolbox and publish a new version.

Pass one or more connection short names as positionals. All removals are
applied atomically: each invocation publishes exactly one new toolbox version.

Refuses to leave the toolbox with zero tools (use 'toolbox delete' instead).

Examples:

  azd ai toolbox connection remove research my-mcp
  azd ai toolbox connection remove research a b c --force
`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnectionRemove(
				cmd.Context(), args[0], args[1:],
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
	ctx context.Context, toolboxName string, connNames []string,
	verb connectionRemoveFlags,
	parent toolboxFlags, resolver connectionResolver,
) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	if len(connNames) == 0 {
		return exterrors.Validation(
			exterrors.CodeInvalidPositionalArg,
			"at least one <connection> must be provided",
			"pass one or more connection short names",
		)
	}
	for _, n := range connNames {
		if strings.TrimSpace(n) == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidPositionalArg,
				"<connection> must not be empty",
				"remove empty entries from the argument list",
			)
		}
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
		toolboxName, connNames, verb, parent)
}

func runConnectionRemoveWith(
	ctx context.Context, client toolboxClient, resolver connectionResolver,
	endpoint, toolboxName string, connNames []string,
	verb connectionRemoveFlags,
	parent toolboxFlags,
) error {
	// Normalize whitespace so callers that pass `" foo "` match the stored
	// entry. Parity with `skill remove`.
	names := make([]string, 0, len(connNames))
	for _, n := range connNames {
		names = append(names, strings.TrimSpace(n))
	}

	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		return toolboxNotFoundOrService(err, toolboxName, exterrors.OpGetToolbox)
	}
	current, err := client.GetToolboxVersion(ctx, toolboxName, tb.DefaultVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	// Resolve each name and strip from the tools[].
	filtered := slices.Clone(current.Tools)
	removedConns := make([]*projectConnection, 0, len(names))
	for _, name := range names {
		conn, err := resolver.resolveConnection(ctx, endpoint, name)
		if err != nil {
			return err
		}
		var didRemove bool
		filtered, didRemove = filterOutConnection(filtered, conn.ID)
		if !didRemove {
			return exterrors.Validation(
				exterrors.CodeConnectionNotInToolbox,
				fmt.Sprintf(
					"connection %q is not attached to toolbox %q's current default version",
					name, toolboxName,
				),
				fmt.Sprintf("run 'azd ai toolbox connection list %q'", toolboxName),
			)
		}
		removedConns = append(removedConns, conn)
	}
	if len(filtered) == 0 {
		return exterrors.Validation(
			exterrors.CodeLastToolRemoval,
			fmt.Sprintf(
				"removing the listed connections would leave toolbox %q with zero tools",
				toolboxName,
			),
			fmt.Sprintf(
				"delete the toolbox with `azd ai toolbox delete %q` instead",
				toolboxName,
			),
		)
	}

	if !verb.force {
		shouldProceed := true
		summary := summarizeConnectionNames(removedConns)
		err := withAzdClient(func(azdClient *azdext.AzdClient) error {
			confirmed, err := confirmToolboxDelete(
				ctx,
				azdClient,
				fmt.Sprintf(
					"Detach %s from toolbox %q (publishes a new version)?",
					summary, toolboxName,
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
		Skills:      current.Skills,
		Policies:    current.Policies,
	}
	created, err := client.CreateToolboxVersion(ctx, toolboxName, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
	}

	return emitConnectionRemoveResult(toolboxName, created.Version, removedConns, parent.output)
}

// summarizeConnectionNames renders "connection \"a\"" or "connections [\"a\", \"b\"]".
func summarizeConnectionNames(conns []*projectConnection) string {
	if len(conns) == 1 {
		return fmt.Sprintf("connection %q", conns[0].Name)
	}
	quoted := make([]string, 0, len(conns))
	for _, c := range conns {
		quoted = append(quoted, fmt.Sprintf("%q", c.Name))
	}
	return "connections [" + strings.Join(quoted, ", ") + "]"
}

func emitConnectionRemoveResult(
	toolboxName, newVersion string, conns []*projectConnection, output string,
) error {
	if output == "json" {
		if len(conns) == 1 {
			return emitJSON(map[string]any{
				"toolbox":       toolboxName,
				"version":       newVersion,
				"connection":    conns[0].Name,
				"connection_id": conns[0].ID,
			})
		}
		rows := make([]map[string]string, 0, len(conns))
		for _, c := range conns {
			rows = append(rows, map[string]string{
				"connection":    c.Name,
				"connection_id": c.ID,
			})
		}
		return emitJSON(map[string]any{
			"toolbox":     toolboxName,
			"version":     newVersion,
			"connections": rows,
		})
	}
	if len(conns) == 1 {
		fmt.Printf(
			"Published toolbox %s version %s (detached connection %s).\n",
			toolboxName, newVersion, conns[0].Name,
		)
	} else {
		names := make([]string, 0, len(conns))
		for _, c := range conns {
			names = append(names, c.Name)
		}
		fmt.Printf(
			"Published toolbox %s version %s (detached connections [%s]).\n",
			toolboxName, newVersion, strings.Join(names, ", "),
		)
	}
	fmt.Printf("The default version is unchanged; "+
		"run `azd ai toolbox update %q --default-version %q` to promote.\n", toolboxName, newVersion)
	return nil
}
