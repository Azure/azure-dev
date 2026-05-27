// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"

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
		Use:   "remove <toolbox> <skill>",
		Short: "Detach a skill reference from a toolbox.",
		Long: `Detach a skill reference from a toolbox.

Publishes a new default version with the named skill stripped. Removing the
last skill is allowed.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillRemove(
				cmd.Context(), args[0], args[1], *flags, readToolboxFlags(cmd, extCtx),
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
	ctx context.Context, toolboxName, skillName string,
	verb skillRemoveFlags, parent toolboxFlags,
) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateSkillName(skillName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
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

	return runSkillRemoveWith(ctx, client, toolboxName, skillName, verb, parent)
}

// runSkillRemoveWith is the testable core.
func runSkillRemoveWith(
	ctx context.Context, client toolboxClient,
	toolboxName, skillName string,
	verb skillRemoveFlags, parent toolboxFlags,
) error {
	tb, err := client.GetToolbox(ctx, toolboxName)
	if err != nil {
		return toolboxNotFoundOrService(err, toolboxName, exterrors.OpGetToolbox)
	}
	current, err := client.GetToolboxVersion(ctx, toolboxName, tb.DefaultVersion)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetToolboxVersion)
	}

	filtered, removed := filterOutSkill(current.Skills, skillName)
	if !removed {
		return exterrors.Validation(
			exterrors.CodeSkillNotInToolbox,
			fmt.Sprintf(
				"skill %q is not attached to toolbox %q's current default version",
				skillName, toolboxName,
			),
			fmt.Sprintf("run 'azd ai toolbox skill list %q'", toolboxName),
		)
	}

	if !verb.force {
		shouldProceed := true
		err := withAzdClient(func(azdClient *azdext.AzdClient) error {
			confirmed, err := confirmToolboxDelete(
				ctx,
				azdClient,
				fmt.Sprintf(
					"Detach skill %q from toolbox %q (publishes a new version)?",
					skillName, toolboxName,
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

	return emitSkillRemoveResult(toolboxName, created.Version, skillName, parent.output)
}

func emitSkillRemoveResult(toolboxName, newVersion, skillName, output string) error {
	if output == "json" {
		return emitJSON(map[string]any{
			"toolbox": toolboxName,
			"version": newVersion,
			"skill":   skillName,
		})
	}
	fmt.Printf(
		"Detached skill %s from toolbox %s (now at version %s).\n",
		skillName, toolboxName, newVersion,
	)
	return nil
}
