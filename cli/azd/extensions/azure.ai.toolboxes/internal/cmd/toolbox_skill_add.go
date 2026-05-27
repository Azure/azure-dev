// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"slices"

	"azure.ai.toolboxes/internal/exterrors"
	"azure.ai.toolboxes/internal/pkg/azure"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// newToolboxSkillAddCommand returns the `skill add` command.
func newToolboxSkillAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)

	cmd := &cobra.Command{
		Use:   "add <toolbox> <skill>[@<version>]",
		Short: "Attach a skill reference to a toolbox.",
		Long: `Attach a skill reference to a toolbox.

Publishes a new default version with the skill appended. When the version is
omitted, the reference resolves to the skill's default version at read time.

Examples:

  azd ai toolbox skill add research my-skill
  azd ai toolbox skill add research my-skill@2
`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillAdd(cmd.Context(), args[0], args[1], readToolboxFlags(cmd, extCtx))
		},
	}
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runSkillAdd(ctx context.Context, toolboxName, rawSkill string, parent toolboxFlags) error {
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
	logResolvedEndpoint("toolbox skill add", resolved)

	return runSkillAddWith(ctx, client, toolboxName, rawSkill, parent)
}

// runSkillAddWith is the testable core.
func runSkillAddWith(
	ctx context.Context, client toolboxClient,
	toolboxName, rawSkill string, parent toolboxFlags,
) error {
	spec, err := parseSkillFlag(rawSkill)
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

	if findSkillEntry(current.Skills, spec.Name) >= 0 {
		return exterrors.Validation(
			exterrors.CodeSkillAlreadyAttached,
			fmt.Sprintf(
				"skill %q is already attached to toolbox %q's current default version",
				spec.Name, toolboxName,
			),
			fmt.Sprintf(
				"remove the existing reference with `azd ai toolbox skill remove %q %q` first",
				toolboxName, spec.Name,
			),
		)
	}

	newSkills := slices.Clone(current.Skills)
	newSkills = append(newSkills, buildSkillEntry(spec))

	req := &azure.CreateToolboxVersionRequest{
		Description: current.Description,
		Metadata:    current.Metadata,
		Tools:       current.Tools,
		Skills:      newSkills,
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

	return emitSkillAddResult(toolboxName, created.Version, spec, parent.output)
}

func emitSkillAddResult(toolboxName, newVersion string, spec skillSpec, output string) error {
	if output == "json" {
		payload := map[string]any{
			"toolbox": toolboxName,
			"version": newVersion,
			"skill":   spec.Name,
		}
		if spec.Version != "" {
			payload["skill_version"] = spec.Version
		}
		return emitJSON(payload)
	}

	pinned := ""
	if spec.Version != "" {
		pinned = "@" + spec.Version
	}
	fmt.Printf(
		"Attached skill %s%s to toolbox %s (now at version %s).\n",
		spec.Name, pinned, toolboxName, newVersion,
	)
	return nil
}
