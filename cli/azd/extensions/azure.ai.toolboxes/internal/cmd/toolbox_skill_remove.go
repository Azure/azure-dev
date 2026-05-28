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

// skillRemoveFlags carries the verb-specific flags for `skill remove`.
type skillRemoveFlags struct {
	force bool
}

// newToolboxSkillRemoveCommand returns the `skill remove` command.
func newToolboxSkillRemoveCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &skillRemoveFlags{}

	cmd := &cobra.Command{
		Use:   "remove <toolbox> <skill>...",
		Short: "Detach one or more skill references from a toolbox.",
		Long: `Detach one or more skill references from a toolbox and publish a new version.

Pass one or more skill short names as positionals. All removals are applied
atomically: each invocation publishes exactly one new toolbox version.

Removing the last skill is allowed.

Examples:

  azd ai toolbox skill remove research my-skill
  azd ai toolbox skill remove research a b c --force
`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillRemove(
				cmd.Context(), args[0], args[1:], *flags, readToolboxFlags(cmd, extCtx),
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

func runSkillRemove(
	ctx context.Context, toolboxName string, skillNames []string,
	verb skillRemoveFlags, parent toolboxFlags,
) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	if len(skillNames) == 0 {
		return exterrors.Validation(
			exterrors.CodeInvalidPositionalArg,
			"at least one <skill> must be provided",
			"pass one or more skill short names",
		)
	}
	for _, n := range skillNames {
		if err := validateSkillName(n); err != nil {
			return err
		}
	}
	if parent.noPrompt && !verb.force {
		return exterrors.Validation(
			exterrors.CodeMissingForceFlag,
			"--no-prompt requires --force for skill removal",
			"add --force to confirm the operation non-interactively",
		)
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox skill remove", resolved)

	return runSkillRemoveWith(ctx, client, toolboxName, skillNames, verb, parent)
}

// runSkillRemoveWith is the testable core.
func runSkillRemoveWith(
	ctx context.Context, client toolboxClient,
	toolboxName string, skillNames []string,
	verb skillRemoveFlags, parent toolboxFlags,
) error {
	// Normalize whitespace so `" beta "` matches the stored entry.
	names := make([]string, 0, len(skillNames))
	for _, n := range skillNames {
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

	filtered := slices.Clone(current.Skills)
	for _, name := range names {
		var didRemove bool
		filtered, didRemove = filterOutSkill(filtered, name)
		if !didRemove {
			return exterrors.Validation(
				exterrors.CodeSkillNotInToolbox,
				fmt.Sprintf(
					"skill %q is not attached to toolbox %q's current default version",
					name, toolboxName,
				),
				fmt.Sprintf("run 'azd ai toolbox skill list %q'", toolboxName),
			)
		}
	}

	if !verb.force {
		shouldProceed := true
		summary := summarizeSkillNames(names)
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
		Tools:       current.Tools,
		Skills:      filtered,
	}
	created, err := client.CreateToolboxVersion(ctx, toolboxName, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateToolboxVersion)
	}

	return emitSkillRemoveResult(toolboxName, created.Version, names, parent.output)
}

// summarizeSkillNames renders "skill \"a\"" or "skills [\"a\", \"b\"]".
func summarizeSkillNames(names []string) string {
	if len(names) == 1 {
		return fmt.Sprintf("skill %q", names[0])
	}
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, fmt.Sprintf("%q", n))
	}
	return "skills [" + strings.Join(quoted, ", ") + "]"
}

func emitSkillRemoveResult(toolboxName, newVersion string, names []string, output string) error {
	if output == "json" {
		if len(names) == 1 {
			return emitJSON(map[string]any{
				"toolbox": toolboxName,
				"version": newVersion,
				"skill":   names[0],
			})
		}
		return emitJSON(map[string]any{
			"toolbox": toolboxName,
			"version": newVersion,
			"skills":  names,
		})
	}
	if len(names) == 1 {
		fmt.Printf(
			"Published toolbox %s version %s (detached skill %s).\n",
			toolboxName, newVersion, names[0],
		)
	} else {
		fmt.Printf(
			"Published toolbox %s version %s (detached skills [%s]).\n",
			toolboxName, newVersion, strings.Join(names, ", "),
		)
	}
	fmt.Printf("The default version is unchanged; "+
		"run `azd ai toolbox update %q --default-version %q` to promote.\n", toolboxName, newVersion)
	return nil
}
