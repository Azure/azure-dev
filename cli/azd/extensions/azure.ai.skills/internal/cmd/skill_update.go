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

// updateAction implements `azd ai skill update`. Skills are versioned, so an
// "update" creates a new immutable version and sets it as the default. To
// repoint default_version at an existing version without uploading new
// content, pass --set-default-version <ver>.
type updateFlags struct {
	name            string
	description     string
	instructions    string
	file            string
	setDefault      string
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

	// --set-default-version is a metadata-only update: POST /skills/{name}
	// with { default_version }. No new version is created.
	if a.flags.setDefault != "" {
		updated, err := skillCtx.client.UpdateSkillDefaultVersion(ctx, a.flags.name, a.flags.setDefault)
		if err != nil {
			return exterrors.ServiceFromAzure(err, exterrors.OpUpdateSkill)
		}
		if a.flags.output == outputJSON {
			return printJSON(updated)
		}
		fmt.Printf("Skill %q default_version set to %q.\n", updated.Name, updated.DefaultVersion)
		return printSkillDetail(updated, outputTable)
	}

	content, err := a.buildInlineContent()
	if err != nil {
		return err
	}

	version, err := skillCtx.client.CreateVersionInline(ctx, a.flags.name, skill_api.CreateVersionRequest{
		InlineContent: content,
		Default:       true,
	})
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateSkill)
	}

	if a.flags.output == outputJSON {
		return printJSON(version)
	}
	fmt.Printf("Skill %q updated; new version %q is now the default.\n", a.flags.name, version.Version)
	skill, err := skillCtx.client.GetSkill(ctx, a.flags.name)
	if err != nil {
		return printSkillVersionDetail(version, outputTable)
	}
	return printSkillDetail(skill, outputTable)
}

func (a *updateAction) buildInlineContent() (*skill_api.SkillInlineContent, error) {
	content := &skill_api.SkillInlineContent{
		Description:  a.flags.description,
		Instructions: a.flags.instructions,
	}

	if a.flags.file != "" {
		data, readErr := readFileWithLimit(a.flags.file)
		if readErr != nil {
			return nil, readErr
		}
		parsed, parseErr := skill_api.ParseSkillMd(data)
		if parseErr != nil {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				fmt.Sprintf("failed to parse %s: %s", a.flags.file, parseErr),
				"ensure the file begins with a YAML front matter block delimited by '---'",
			)
		}
		content.Description = parsed.Description
		content.Instructions = parsed.Instructions
		content.Metadata = parsed.Metadata
	}

	if strings.TrimSpace(content.Description) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"update requires a non-empty description",
			"pass --description, or use --file <path> with a SKILL.md that supplies one",
		)
	}
	if strings.TrimSpace(content.Instructions) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"update requires non-empty instructions",
			"pass --instructions, or use --file <path> with a SKILL.md body",
		)
	}
	return content, nil
}

func (a *updateAction) validateFlags() error {
	inlineProvided := a.flags.descriptionSet || a.flags.instructionsSet
	fileProvided := a.flags.file != ""
	setDefaultProvided := a.flags.setDefault != ""

	// --set-default-version is mutually exclusive with content flags.
	if setDefaultProvided && (inlineProvided || fileProvided) {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--set-default-version cannot be combined with --description / --instructions / --file",
			"pass --set-default-version on its own, or omit it to create a new default version",
		)
	}
	if setDefaultProvided {
		return nil
	}

	if !inlineProvided && !fileProvided {
		return exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"no fields to update",
			"pass --description, --instructions, and/or --file <path>; or use --set-default-version <ver>",
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
				"use `azd ai skill create <name> --file <path>.zip --force` to replace the skill "+
					"(this deletes the existing skill and all of its versions first)",
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
		Short: "Create a new default version for a Foundry skill.",
		Long: `Skills are versioned and immutable. ` + "`update`" + ` creates a new version from
inline content (--description / --instructions) or a SKILL.md file and sets
it as the skill's new default version.

To repoint default_version at an existing version without uploading new
content, pass --set-default-version <version>.

ZIP packages are not accepted here. To replace the entire skill (deleting all
existing versions), use ` + "`azd ai skill create <name> --file <archive>.zip --force`" + `.`,
		Example: `  azd ai skill update my-skill --description "Updated summary" --instructions "..."
  azd ai skill update my-skill --file ./SKILL.md
  azd ai skill update my-skill --set-default-version 1`,
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

	cmd.Flags().StringVar(&flags.description, "description", "", "New human-readable summary for the next version")
	cmd.Flags().StringVar(&flags.instructions, "instructions", "", "New Markdown instructions body for the next version")
	cmd.Flags().StringVar(&flags.file, "file", "", "Path to a SKILL.md file whose values become the next version's inline content")
	cmd.Flags().StringVar(&flags.setDefault, "set-default-version", "", "Set the skill's default_version to an existing version without uploading new content")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputJSON,
	})
	return cmd
}
