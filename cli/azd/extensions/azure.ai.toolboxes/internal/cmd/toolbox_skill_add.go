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

// skillAddFlags carries the verb-specific flags for `skill add`.
type skillAddFlags struct {
	fromFile string
}

// newToolboxSkillAddCommand returns the `skill add` command.
func newToolboxSkillAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &skillAddFlags{}

	cmd := &cobra.Command{
		Use:   "add <toolbox> [skill[@version]]",
		Short: "Attach one or more skill references to a toolbox.",
		Long: `Attach one or more skill references to a toolbox.

Pass a single skill as the positional, or many via --from-file. Either way
the invocation publishes exactly one new toolbox version. The toolbox's
default version is unchanged; run
'azd ai toolbox update <toolbox> --default-version <version>' to promote it.

When the version is omitted, the reference resolves to the skill's default
version at read time.

Examples:

  azd ai toolbox skill add research my-skill
  azd ai toolbox skill add research my-skill@2
  azd ai toolbox skill add research --from-file ./skills.yaml
`,
		Args: func(cmd *cobra.Command, args []string) error {
			fromFile, _ := cmd.Flags().GetString("from-file")
			if strings.TrimSpace(fromFile) != "" {
				if len(args) != 1 {
					return cobra.ExactArgs(1)(cmd, args)
				}
				return nil
			}
			if len(args) != 2 {
				return cobra.RangeArgs(2, 2)(cmd, args)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rawSkill := ""
			if len(args) > 1 {
				rawSkill = args[1]
			}
			return runSkillAdd(cmd.Context(), args[0], rawSkill, *flags, readToolboxFlags(cmd, extCtx))
		},
	}
	cmd.Flags().StringVar(
		&flags.fromFile, "from-file", "",
		"Path to a JSON/YAML file listing skills to attach (skills[] block).",
	)
	registerToolboxOutputFlag(cmd)
	return cmd
}

func runSkillAdd(
	ctx context.Context, toolboxName, rawSkill string,
	verb skillAddFlags, parent toolboxFlags,
) error {
	if err := validateToolboxName(toolboxName); err != nil {
		return err
	}
	if err := validateOutputFormat(parent.output); err != nil {
		return err
	}
	hasFile := strings.TrimSpace(verb.fromFile) != ""
	hasPos := strings.TrimSpace(rawSkill) != ""
	if hasFile && hasPos {
		return exterrors.Validation(
			exterrors.CodeInvalidPositionalArg,
			"do not pass <skill> when --from-file is set",
			"either pass a single skill positional or use --from-file",
		)
	}
	if !hasFile && !hasPos {
		return exterrors.Validation(
			exterrors.CodeInvalidPositionalArg,
			"<skill> must not be empty",
			"pass a skill name or use --from-file",
		)
	}

	client, resolved, err := resolveToolboxAndClient(ctx, parent)
	if err != nil {
		return err
	}
	logResolvedEndpoint("toolbox skill add", resolved)

	return runSkillAddWith(ctx, client, toolboxName, rawSkill, verb, parent)
}

// runSkillAddWith is the testable core.
func runSkillAddWith(
	ctx context.Context, client toolboxClient,
	toolboxName, rawSkill string,
	verb skillAddFlags, parent toolboxFlags,
) error {
	specs, err := collectSkillSpecs(rawSkill, verb)
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

	// Reject duplicates within the input and against the current default.
	seen := map[string]struct{}{}
	for _, sk := range current.Skills {
		if n, ok := sk["name"].(string); ok && n != "" {
			seen[n] = struct{}{}
		}
	}
	for _, sp := range specs {
		if _, dup := seen[sp.Name]; dup {
			return exterrors.Validation(
				exterrors.CodeSkillAlreadyAttached,
				fmt.Sprintf(
					"skill %q is already attached to toolbox %q's current default version "+
						"(or appears more than once in the input)",
					sp.Name, toolboxName,
				),
				fmt.Sprintf(
					"remove the existing reference with `azd ai toolbox skill remove %q %q` first",
					toolboxName, sp.Name,
				),
			)
		}
		seen[sp.Name] = struct{}{}
	}

	newSkills := slices.Clone(current.Skills)
	for _, sp := range specs {
		newSkills = append(newSkills, buildSkillEntry(sp))
	}

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

	return emitSkillAddResult(toolboxName, created.Version, specs, parent.output)
}

// collectSkillSpecs picks the active input mode and returns the parsed list.
func collectSkillSpecs(rawSkill string, verb skillAddFlags) ([]skillSpec, error) {
	if strings.TrimSpace(verb.fromFile) != "" {
		var input toolboxSkillsFile
		if err := parseToolboxFile(verb.fromFile, &input); err != nil {
			return nil, err
		}
		if len(input.Skills) == 0 {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"no skills to add",
				"provide at least one skill in 'skills[]'",
			)
		}
		specs := make([]skillSpec, 0, len(input.Skills))
		for _, s := range input.Skills {
			if err := validateSkillName(s.Name); err != nil {
				return nil, err
			}
			specs = append(specs, skillSpec{
				Name:    strings.TrimSpace(s.Name),
				Version: strings.TrimSpace(s.Version),
			})
		}
		return specs, nil
	}
	sp, err := parseSkillFlag(rawSkill)
	if err != nil {
		return nil, err
	}
	return []skillSpec{sp}, nil
}

func emitSkillAddResult(toolboxName, newVersion string, specs []skillSpec, output string) error {
	if output == "json" {
		if len(specs) == 1 {
			payload := map[string]any{
				"toolbox": toolboxName,
				"version": newVersion,
				"skill":   specs[0].Name,
			}
			if specs[0].Version != "" {
				payload["skill_version"] = specs[0].Version
			}
			return emitJSON(payload)
		}
		rows := make([]map[string]any, 0, len(specs))
		for _, s := range specs {
			row := map[string]any{"name": s.Name}
			if s.Version != "" {
				row["version"] = s.Version
			}
			rows = append(rows, row)
		}
		return emitJSON(map[string]any{
			"toolbox": toolboxName,
			"version": newVersion,
			"skills":  rows,
		})
	}

	if len(specs) == 1 {
		pinned := ""
		if specs[0].Version != "" {
			pinned = "@" + specs[0].Version
		}
		fmt.Printf(
			"Published toolbox %s version %s (attached skill %s%s).\n",
			toolboxName, newVersion, specs[0].Name, pinned,
		)
	} else {
		names := make([]string, 0, len(specs))
		for _, s := range specs {
			entry := s.Name
			if s.Version != "" {
				entry += "@" + s.Version
			}
			names = append(names, entry)
		}
		fmt.Printf(
			"Published toolbox %s version %s (attached skills [%s]).\n",
			toolboxName, newVersion, strings.Join(names, ", "),
		)
	}
	fmt.Printf("The default version is unchanged; "+
		"run `azd ai toolbox update %q --default-version %q` to promote.\n", toolboxName, newVersion)
	return nil
}
