// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type updateFlags struct {
	name            string
	description     string
	instructions    string
	file            string
	output          string
	projectEndpoint string

	descriptionSet  bool
	instructionsSet bool
}

type updateAction struct{ flags *updateFlags }

func (a *updateAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}
	if err := a.validateFlags(); err != nil {
		return err
	}

	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}

	// GET-merge-POST so a single-field update doesn't drop the others.
	current, err := skillCtx.client.Get(ctx, a.flags.name)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetSkill)
	}

	req := skill_api.UpdateRequest{
		Description: current.Description,
		Metadata:    current.Metadata,
	}
	if a.flags.descriptionSet {
		req.Description = a.flags.description
	}
	if a.flags.instructionsSet {
		req.Instructions = a.flags.instructions
	}

	if a.flags.file != "" {
		data, readErr := readFileWithLimit(a.flags.file)
		if readErr != nil {
			return readErr
		}
		parsed, parseErr := skill_api.ParseSkillMd(data)
		if parseErr != nil {
			return exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				fmt.Sprintf("failed to parse %s: %s", a.flags.file, parseErr),
				"ensure the file begins with a YAML front matter block delimited by '---'",
			)
		}
		if parsed.Description != "" {
			req.Description = parsed.Description
		}
		if parsed.Instructions != "" {
			req.Instructions = parsed.Instructions
		}
		if len(parsed.Metadata) > 0 {
			req.Metadata = parsed.Metadata
		}
	}

	updated, err := skillCtx.client.Update(ctx, a.flags.name, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateSkill)
	}

	if a.flags.output == outputJSON {
		return printJSON(updated)
	}
	fmt.Printf("Skill %q updated.\n", updated.Name)
	return printSkillDetail(updated, outputTable)
}

func (a *updateAction) validateFlags() error {
	inlineProvided := a.flags.descriptionSet || a.flags.instructionsSet
	fileProvided := a.flags.file != ""

	if !inlineProvided && !fileProvided {
		return exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"no fields to update",
			"pass --description, --instructions, and/or --file <path>",
		)
	}
	if inlineProvided && fileProvided {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--file is mutually exclusive with --description / --instructions on update",
			"pass either inline flags or --file <path>, not both",
		)
	}

	if fileProvided {
		ext := strings.ToLower(filepath.Ext(a.flags.file))
		switch ext {
		case ".md":
			return nil
		case ".zip":
			return exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				"ZIP packages cannot be applied via `skill update`",
				"use `azd ai skill create <name> --file <path>.zip --force` to replace the skill",
			)
		default:
			return exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				fmt.Sprintf("unsupported --file extension %q on update", ext),
				"update only accepts .md files",
			)
		}
	}
	return nil
}

func newUpdateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &updateFlags{}
	action := &updateAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Update an existing Foundry skill.",
		Long: `Update an existing skill's description, instructions, or metadata.

Pass any subset of:
  --description "..."  --instructions "..."
or:
  --file ./SKILL.md    (parsed locally)

The CLI fetches the current skill, merges your changes locally, then POSTs the
merged payload to the service.

ZIP packages are not accepted here. To replace a skill's package, use
` + "`azd ai skill create <name> --file <archive>.zip --force`" + `. Skills are not
versioned, so that path is destructive: it deletes the existing skill before
re-creating it from the archive.`,
		Example: `  azd ai skill update my-skill --description "Updated summary"
  azd ai skill update my-skill --file ./SKILL.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.descriptionSet = cmd.Flags().Changed("description")
			flags.instructionsSet = cmd.Flags().Changed("instructions")
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	cmd.Flags().StringVar(&flags.description, "description", "", "New human-readable summary")
	cmd.Flags().StringVar(&flags.instructions, "instructions", "", "New Markdown instructions body")
	cmd.Flags().StringVar(&flags.file, "file", "", "Path to a SKILL.md file whose values override the current skill")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputJSON,
	})
	return cmd
}
