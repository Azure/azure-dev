// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// createFlags holds parsed input for the `skill create` command.
type createFlags struct {
	name            string
	description     string
	instructions    string
	file            string
	force           bool
	noPrompt        bool
	output          string
	projectEndpoint string

	descriptionSet  bool
	instructionsSet bool
}

// createAction is the create-command implementation.
type createAction struct {
	flags *createFlags
}

// Run executes the create operation.
func (a *createAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}

	mode, err := selectCreateMode(a.flags)
	if err != nil {
		return err
	}

	// Resolve interactive prompts (mode==modeNone with prompting available)
	// before doing any IO so the user sees a fast validation error if neither
	// inline nor file mode was supplied with --no-prompt.
	if mode == modeNone {
		if a.flags.noPrompt {
			return exterrors.Validation(
				exterrors.CodeMissingRequiredField,
				"no input supplied to skill create",
				"pass --description and --instructions, or --file <path>",
			)
		}
		if err := promptForInline(ctx, a.flags); err != nil {
			return err
		}
		mode = modeInline
	}

	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}

	// Honor --force by deleting the existing skill first.
	if a.flags.force {
		if _, delErr := skillCtx.client.Delete(ctx, a.flags.name); delErr != nil {
			// Only swallow 404 — anything else means the upcoming create would
			// likely fail too, so surface it now.
			if !isNotFound(delErr) {
				return exterrors.ServiceFromAzure(delErr, exterrors.OpDeleteSkill)
			}
		}
	}

	switch mode {
	case modeInline:
		return a.runInline(ctx, skillCtx.client)
	case modeFileMd:
		return a.runFileMd(ctx, skillCtx.client)
	case modeFilePackage:
		return a.runFilePackage(ctx, skillCtx.client)
	}
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"unsupported create mode",
		"this is a bug; please file an issue",
	)
}

func (a *createAction) runInline(ctx context.Context, client *skill_api.Client) error {
	if strings.TrimSpace(a.flags.description) == "" {
		return exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"inline mode requires --description",
			"pass --description and --instructions, or use --file <path>",
		)
	}
	if strings.TrimSpace(a.flags.instructions) == "" {
		return exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"inline mode requires --instructions",
			"pass --description and --instructions, or use --file <path>",
		)
	}

	created, err := client.CreateInline(ctx, skill_api.CreateRequest{
		Name:         a.flags.name,
		Description:  a.flags.description,
		Instructions: a.flags.instructions,
	})
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateSkill)
	}
	return printCreateResult(created, a.flags.output)
}

func (a *createAction) runFileMd(ctx context.Context, client *skill_api.Client) error {
	data, err := readFileWithLimit(a.flags.file)
	if err != nil {
		return err
	}
	parsed, parseErr := skill_api.ParseSkillMd(data)
	if parseErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("failed to parse %s: %s", a.flags.file, parseErr),
			"ensure the file begins with a YAML front matter block delimited by '---'",
		)
	}

	if parsed.Name != "" && parsed.Name != a.flags.name && !shouldSuppressWarning(a.flags.noPrompt, a.flags.output) {
		fmt.Fprintf(os.Stderr,
			"Warning: SKILL.md front matter `name: %q` does not match positional argument %q; using %q\n",
			parsed.Name, a.flags.name, a.flags.name,
		)
	}

	req := skill_api.CreateRequest{
		Name:         a.flags.name,
		Description:  parsed.Description,
		Instructions: parsed.Instructions,
		Metadata:     parsed.Metadata,
	}
	created, err := client.CreateInline(ctx, req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateSkill)
	}
	return printCreateResult(created, a.flags.output)
}

func (a *createAction) runFilePackage(ctx context.Context, client *skill_api.Client) error {
	info, statErr := os.Stat(a.flags.file)
	if statErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot stat %s: %s", a.flags.file, statErr),
			"verify the path exists and is readable",
		)
	}
	if info.IsDir() {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("--file %s is a directory; expected a .tar.gz / .tgz archive", a.flags.file),
			"pass a single gzip archive path",
		)
	}

	f, openErr := os.Open(a.flags.file) //nolint:gosec // path supplied by the user, opened for the user
	if openErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot open %s: %s", a.flags.file, openErr),
			"verify the file is readable",
		)
	}
	defer f.Close()

	created, err := client.CreatePackage(ctx, f, info.Size())
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateSkill)
	}
	return printCreateResult(created, a.flags.output)
}

func printCreateResult(s *skill_api.Skill, format string) error {
	if format == outputJSON {
		return printJSON(s)
	}
	fmt.Printf("Skill %q created.\n", s.Name)
	return printSkillDetail(s, outputTable)
}

// --- mode selection ---

type createMode int

const (
	modeNone createMode = iota
	modeInline
	modeFileMd
	modeFilePackage
)

func selectCreateMode(f *createFlags) (createMode, error) {
	inlineProvided := f.descriptionSet || f.instructionsSet
	fileProvided := f.file != ""

	if inlineProvided && fileProvided {
		return modeNone, exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--file is mutually exclusive with --description / --instructions",
			"pass only one of: inline flags, --file <path>",
		)
	}

	if fileProvided {
		ext := strings.ToLower(filepath.Ext(f.file))
		// `.tgz` and `.tar.gz` both work for packages; `.md` for inline.
		switch {
		case ext == ".md":
			return modeFileMd, nil
		case ext == ".tgz":
			return modeFilePackage, nil
		case strings.HasSuffix(strings.ToLower(f.file), ".tar.gz"):
			return modeFilePackage, nil
		default:
			return modeNone, exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				fmt.Sprintf("unsupported --file extension %q", ext),
				"use .md for inline metadata, or .tar.gz / .tgz for a package upload",
			)
		}
	}

	if inlineProvided {
		return modeInline, nil
	}
	return modeNone, nil
}

func promptForInline(ctx context.Context, f *createFlags) error {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"no input supplied to skill create",
			"pass --description and --instructions, or --file <path>",
		)
	}
	defer azdClient.Close()

	if strings.TrimSpace(f.description) == "" {
		resp, promptErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message: "Skill description",
				HelpMessage: "A short human-readable summary of what this skill " +
					"does. Sent to the service as `description`.",
				Required: true,
			},
		})
		if promptErr != nil {
			return promptErr
		}
		f.description = resp.Value
		f.descriptionSet = true
	}

	if strings.TrimSpace(f.instructions) == "" {
		resp, promptErr := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message: "Skill instructions (Markdown body)",
				HelpMessage: "The Markdown body that defines the skill's " +
					"behavior. Sent to the service as `instructions`.",
				Required: true,
			},
		})
		if promptErr != nil {
			return promptErr
		}
		f.instructions = resp.Value
		f.instructionsSet = true
	}

	return nil
}

// newCreateCommand constructs the `skill create` Cobra command.
func newCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &createFlags{}
	action := &createAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new Foundry skill.",
		Long: `Create a new Foundry skill in one of three mutually exclusive modes:

  1. Inline:   --description "..." --instructions "..."
  2. SKILL.md: --file ./SKILL.md   (CLI parses YAML front matter + body)
  3. Package:  --file ./skill.tar.gz   (CLI uploads the archive as-is)

Pass --force to delete an existing skill of the same name before creating.`,
		Example: `  azd ai skill create greet-user --description "Welcomes a new user" --instructions "Greet ..."
  azd ai skill create greet-user --file ./SKILL.md
  azd ai skill create greet-user --file ./skill.tar.gz --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt
			flags.descriptionSet = cmd.Flags().Changed("description")
			flags.instructionsSet = cmd.Flags().Changed("instructions")
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")

			ctx := azdext.WithAccessToken(cmd.Context())
			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&flags.description, "description", "",
		"Inline mode: human-readable summary of the skill")
	cmd.Flags().StringVar(&flags.instructions, "instructions", "",
		"Inline mode: Markdown body defining skill behavior")
	cmd.Flags().StringVar(&flags.file, "file", "",
		"Path to SKILL.md (.md) or a gzip package (.tar.gz / .tgz)")
	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Delete an existing skill of the same name before creating")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputJSON,
	})
	return cmd
}

// --- shared helpers ---

// readFileWithLimit reads up to 1 MiB from path. SKILL.md should be well under
// the service's 100 KiB+1 KiB description/instruction caps, so 1 MiB is a
// generous bound that still defends against accidentally reading a giant file.
func readFileWithLimit(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot stat %s: %s", path, err),
			"verify the path exists and is readable",
		)
	}
	if info.IsDir() {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("--file %s is a directory; expected a SKILL.md file", path),
			"pass a single .md file",
		)
	}
	const maxBytes = 1 << 20
	if info.Size() > maxBytes {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("%s exceeds the 1 MiB SKILL.md size limit (got %d bytes)", path, info.Size()),
			"split the file into smaller assets and use a package upload",
		)
	}
	data, err := os.ReadFile(path) //nolint:gosec // user-supplied path read on their behalf
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot read %s: %s", path, err),
			"verify the file is readable",
		)
	}
	return data, nil
}

// shouldSuppressWarning reports whether interactive warnings (e.g., front
// matter name mismatch) should be suppressed.
func shouldSuppressWarning(noPrompt bool, format string) bool {
	return noPrompt || format == outputJSON
}

// isNotFound reports whether err looks like an HTTP 404 from the service.
func isNotFound(err error) bool {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode == 404
	}
	return false
}
