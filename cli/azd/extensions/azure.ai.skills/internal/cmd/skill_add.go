// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"azureaiskills/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type addFlags struct {
	name         string
	description  string
	instructions string
	file         string
	output       string
	noPrompt     bool

	descriptionSet  bool
	instructionsSet bool
}

type addAction struct {
	flags       *addFlags
	upsert      func(context.Context, skillServiceDeclaration) (*skillServiceUpsertResult, error)
	writer      io.Writer
	errorWriter io.Writer
}

func (a *addAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}

	declaration, err := a.buildDeclaration()
	if err != nil {
		return err
	}
	if a.upsert == nil {
		return fmt.Errorf("skill service upsert is not configured")
	}
	result, err := a.upsert(ctx, declaration)
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("skill service upsert returned no result")
	}
	writer := a.writer
	if writer == nil {
		writer = io.Discard
	}
	return writeSkillServiceUpsertResult(writer, result, a.flags.output)
}

func (a *addAction) buildDeclaration() (skillServiceDeclaration, error) {
	mode, err := selectCreateMode(&createFlags{
		description:     a.flags.description,
		instructions:    a.flags.instructions,
		file:            a.flags.file,
		descriptionSet:  a.flags.descriptionSet,
		instructionsSet: a.flags.instructionsSet,
	})
	if err != nil {
		return skillServiceDeclaration{}, err
	}

	declaration := skillServiceDeclaration{Name: a.flags.name}
	switch mode {
	case modeInline:
		declaration.Config = skillServiceConfig{
			Description:  a.flags.description,
			Instructions: a.flags.instructions,
		}
	case modeFileMd:
		parsed, err := loadSkillMd(a.flags.file)
		if err != nil {
			return skillServiceDeclaration{}, err
		}
		if parsed.Name != "" &&
			parsed.Name != a.flags.name &&
			!shouldSuppressWarning(a.flags.noPrompt, a.flags.output) {
			errorWriter := a.errorWriter
			if errorWriter == nil {
				errorWriter = io.Discard
			}
			fmt.Fprintf(
				errorWriter,
				"Warning: SKILL.md front matter `name: %q` does not match positional argument %q; using %q\n",
				parsed.Name,
				a.flags.name,
				a.flags.name,
			)
		}
		declaration.Config = skillServiceConfig{
			Description:   parsed.Description,
			Instructions:  parsed.Instructions,
			License:       parsed.License,
			Compatibility: parsed.Compatibility,
			Metadata:      parsed.Metadata,
			Tools:         parsed.AllowedTools,
		}
	case modeFilePackage, modeFileDirectory:
		declaration.ArchiveSource = a.flags.file
	case modeNone:
		return skillServiceDeclaration{}, exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"no content supplied to skill add",
			"pass --description and --instructions, or --file <path>",
		)
	default:
		return skillServiceDeclaration{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"unsupported skill add mode",
			"this is a bug; please file an issue",
		)
	}

	if declaration.ArchiveSource == "" {
		if err := validateSkillServiceConfig(declaration.Name, &declaration.Config); err != nil {
			return skillServiceDeclaration{}, err
		}
	}
	return declaration, nil
}

func writeSkillServiceUpsertResult(
	writer io.Writer,
	result *skillServiceUpsertResult,
	format string,
) error {
	if format == outputJSON {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(result)
	}

	action := "updated"
	if result.Created {
		action = "added"
	}
	_, err := fmt.Fprintf(writer, "Skill service %q %s in azure.yaml.\n", result.Name, action)
	return err
}

func newAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &addFlags{}
	action := &addAction{
		flags:       flags,
		upsert:      upsertSkillServiceToProject,
		writer:      os.Stdout,
		errorWriter: os.Stderr,
	}

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or update a Foundry skill service in azure.yaml.",
		Long: `Add or update a host: azure.ai.skill service in the current azd
project's azure.yaml.

This command is declarative: it only updates azure.yaml and does not create or
modify the remote Foundry skill. Run azd deploy <name> or azd up to reconcile
the service after adding it.

Accepted content shapes:

  1. Inline:    --description "..." --instructions "..."
  2. SKILL.md:  --file ./SKILL.md
  3. Package:   --file ./skill.zip
  4. Directory: --file ./skill-src

Inline and SKILL.md inputs are stored as service properties. ZIP and directory
inputs are stored as portable archive references. Updating an existing skill
service preserves uses:, project:, and fields owned by other extensions.`,
		Example: `  azd ai skill add triage-rules --description "Triage issues" --instructions "Classify each issue."
  azd ai skill add triage-rules --file ./SKILL.md
  azd ai skill add triage-rules --file ./skills/triage-rules
  azd deploy triage-rules`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt
			flags.descriptionSet = cmd.Flags().Changed("description")
			flags.instructionsSet = cmd.Flags().Changed("instructions")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	cmd.Flags().StringVar(&flags.description, "description", "", "Inline mode: human-readable summary of the skill")
	cmd.Flags().StringVar(&flags.instructions, "instructions", "", "Inline mode: Markdown body defining skill behavior")
	cmd.Flags().StringVar(
		&flags.file,
		"file",
		"",
		"Path to SKILL.md, a .zip package, or a directory containing SKILL.md at its root",
	)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputJSON,
	})
	return cmd
}
