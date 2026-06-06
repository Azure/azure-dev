// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"azure.ai.toolboxes/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxSkillListCommand returns the `skill list` command.
func newToolboxSkillListCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "list <toolbox>",
		Short: "List the skill references attached to a toolbox.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillList(cmd.Context(), args[0], readToolboxFlags(cmd, extCtx))
		},
	}
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runSkillList(ctx context.Context, toolboxName string, parent toolboxFlags) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox skill list", resolved)

	return runSkillListWith(ctx, client, toolboxName, parent)
}

func runSkillListWith(
	ctx context.Context, client toolboxClient, toolboxName string, parent toolboxFlags,
) error {
	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		return toolboxNotFoundOrService(err, toolboxName, exterrors.OpGetToolbox)
	}
	version, err := client.GetToolboxVersion(ctx, toolboxName, tb.DefaultVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	rows := extractSkillRows(version.Skills)

	if parent.output == "json" {
		return emitJSON(map[string]any{"skills": rows})
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tVERSION\tTYPE")
	fmt.Fprintln(w, "----\t-------\t----")
	for _, r := range rows {
		ver := r["version"]
		if ver == "" {
			ver = "(default)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", r["name"], ver, r["type"])
	}
	return w.Flush()
}

// extractSkillRows reduces ToolboxSkill discriminator maps to the fields
// surfaced in `skill list` output. Empty version renders as "(default)" in
// table mode.
func extractSkillRows(skills []map[string]any) []map[string]string {
	rows := make([]map[string]string, 0, len(skills))
	for _, s := range skills {
		name, _ := s["name"].(string)
		if name == "" {
			continue
		}
		skType, _ := s["type"].(string)
		ver, _ := s["version"].(string)
		rows = append(rows, map[string]string{
			"name":    name,
			"version": ver,
			"type":    skType,
		})
	}
	return rows
}
