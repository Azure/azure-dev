// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
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

type createAction struct{ flags *createFlags }

func (a *createAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}

	mode, err := selectCreateMode(a.flags)
	if err != nil {
		return err
	}

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

	// --force is destructive: we delete the existing skill by positional name
	// before any guarantee the supplied file targets the same one. For both
	// package (.zip) and SKILL.md inputs, peek the embedded `name` and bail
	// before deletion if it disagrees with the positional argument.
	if a.flags.force {
		if err := verifyFileNameMatches(a.flags.file, a.flags.name, mode); err != nil {
			return err
		}
	}

	if a.flags.force {
		if _, delErr := skillCtx.client.DeleteSkill(ctx, a.flags.name); delErr != nil && !isNotFound(delErr) {
			return exterrors.ServiceFromAzure(delErr, exterrors.OpDeleteSkill)
		}
	}

	switch mode {
	case modeInline:
		return a.runInline(ctx, skillCtx.client)
	case modeFileMd:
		return a.runFileMd(ctx, skillCtx.client)
	case modeFilePackage:
		return a.runFilePackage(ctx, skillCtx.client)
	case modeFileDirectory:
		return a.runFileDirectory(ctx, skillCtx.client)
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

	version, err := client.CreateVersionInline(ctx, a.flags.name, skill_api.CreateVersionRequest{
		InlineContent: &skill_api.SkillInlineContent{
			Description:  a.flags.description,
			Instructions: a.flags.instructions,
		},
		Default: true,
	})
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateSkill)
	}
	return a.printCreateResult(ctx, client, version)
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

	version, err := client.CreateVersionInline(ctx, a.flags.name, skill_api.CreateVersionRequest{
		InlineContent: &skill_api.SkillInlineContent{
			Description:  parsed.Description,
			Instructions: parsed.Instructions,
			Metadata:     parsed.Metadata,
		},
		Default: true,
	})
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateSkill)
	}
	return a.printCreateResult(ctx, client, version)
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
			fmt.Sprintf("--file %s is a directory; expected a .zip archive", a.flags.file),
			"pass a single .zip archive path",
		)
	}

	f, openErr := os.Open(a.flags.file) //nolint:gosec // user-supplied path opened on user's behalf
	if openErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot open %s: %s", a.flags.file, openErr),
			"verify the file is readable",
		)
	}
	defer f.Close()

	version, err := client.CreateVersionFromZip(ctx, a.flags.name, filepath.Base(a.flags.file), f, true)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateSkill)
	}
	return a.printCreateResult(ctx, client, version)
}

// runFileDirectory packages the directory as an in-memory zip and uploads
// it via the same multipart path as runFilePackage. The directory must
// contain a SKILL.md at its root (matching what `azd ai skill download`
// writes by default), so the natural download → edit → create flow works
// without any manual zip step.
func (a *createAction) runFileDirectory(ctx context.Context, client *skill_api.Client) error {
	if _, found, err := skill_api.LocateSkillMdInDir(a.flags.file); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot inspect SKILL.md in %s: %s", a.flags.file, err),
			"verify the directory is readable and SKILL.md is a regular file",
		)
	} else if !found {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("--file %s is a directory without a SKILL.md at its root", a.flags.file),
			"add a SKILL.md to the directory root (matches `azd ai skill download` output) or pass a .zip archive",
		)
	}

	data, archiveErr := skill_api.ArchiveDirectory(a.flags.file, skill_api.ArchiveOptions{})
	if archiveErr != nil {
		return classifyArchiveDirectoryError(archiveErr, a.flags.file)
	}

	archiveName := filepath.Base(filepath.Clean(a.flags.file)) + ".zip"
	version, err := client.CreateVersionFromZip(ctx, a.flags.name, archiveName, bytes.NewReader(data), true)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpCreateSkill)
	}
	return a.printCreateResult(ctx, client, version)
}

// classifyArchiveDirectoryError wraps ArchiveDirectory's sentinel errors in
// structured extension errors so the host prints a useful message.
// Non-sentinel I/O errors (permission denied mid-walk, EvalSymlinks failure,
// TOCTOU file-disappeared, etc.) fall through to the default case so they
// still surface as a structured CodeInvalidSkillFile rather than a raw Go
// error with no Suggestion or telemetry code.
func classifyArchiveDirectoryError(err error, srcDir string) error {
	switch {
	case errors.Is(err, skill_api.ErrUnsafeEntry):
		return exterrors.Validation(
			exterrors.CodeSkillArchiveUnsafe,
			err.Error(),
			"remove symlinks and non-regular files from the directory and re-run",
		)
	case errors.Is(err, skill_api.ErrLimitExceeded):
		return exterrors.Validation(
			exterrors.CodeSkillArchiveUnsafe,
			err.Error(),
			fmt.Sprintf("the contents of %s exceed the per-skill archive safety limit", srcDir),
		)
	case errors.Is(err, skill_api.ErrInvalidArchive):
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			err.Error(),
			"verify the directory exists, is readable, and contains at least one file",
		)
	default:
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("failed to package %s: %s", srcDir, err),
			"verify the directory and its contents are readable",
		)
	}
}

// printCreateResult prints either the created version envelope or, when the
// caller wants a human-readable summary, the freshly-loaded Skill that the
// version belongs to so users see default_version / latest_version.
func (a *createAction) printCreateResult(ctx context.Context, client *skill_api.Client, version *skill_api.SkillVersion) error {
	if a.flags.output == outputJSON {
		return printJSON(version)
	}
	fmt.Printf("Skill %q version %q created.\n", a.flags.name, version.Version)
	skill, err := client.GetSkill(ctx, a.flags.name)
	if err != nil {
		// Don't fail the create just because the follow-up GET failed; fall
		// back to printing the version envelope instead.
		return printSkillVersionDetail(version, outputTable)
	}
	return printSkillDetail(skill, outputTable)
}

// verifyFileNameMatches refuses --force when the user-supplied file's embedded
// `name` disagrees with the positional argument, so a typo can't wipe an
// unrelated skill. Inline-mode (no --file) is always allowed.
func verifyFileNameMatches(filePath, positionalName string, mode createMode) error {
	switch mode {
	case modeFilePackage:
		return verifyPackageNameMatches(filePath, positionalName)
	case modeFileMd:
		return verifyMdNameMatches(filePath, positionalName)
	case modeFileDirectory:
		return verifyDirectoryNameMatches(filePath, positionalName)
	}
	return nil
}

// verifyDirectoryNameMatches reads the directory's SKILL.md and refuses
// --force on a `name` mismatch. Returns nil when SKILL.md omits `name` or
// the names agree.
func verifyDirectoryNameMatches(dirPath, positionalName string) error {
	mdPath, found, err := skill_api.LocateSkillMdInDir(dirPath)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot inspect SKILL.md in %s: %s", dirPath, err),
			"verify the directory is readable and SKILL.md is a regular file",
		)
	}
	if !found {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("--file %s is a directory without a SKILL.md at its root", dirPath),
			"add a SKILL.md to the directory root before re-running with --force",
		)
	}
	return verifyMdNameMatches(mdPath, positionalName)
}

// verifyMdNameMatches reads the SKILL.md front matter and refuses --force on
// a `name` mismatch. Returns nil when SKILL.md omits `name` or the names agree.
func verifyMdNameMatches(filePath, positionalName string) error {
	data, err := readFileWithLimit(filePath)
	if err != nil {
		return err
	}
	parsed, parseErr := skill_api.ParseSkillMd(data)
	if parseErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("failed to parse %s: %s", filePath, parseErr),
			"ensure the file begins with a YAML front matter block delimited by '---'",
		)
	}
	if parsed.Name == "" || parsed.Name == positionalName {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeInvalidSkillFile,
		fmt.Sprintf(
			"--force refused: SKILL.md declares name %q which does not match positional argument %q",
			parsed.Name, positionalName,
		),
		"re-run without --force, or fix the positional name / SKILL.md so they agree",
	)
}

// verifyPackageNameMatches refuses --force when the archive's SKILL.md
// declares a different `name` than the positional argument, to avoid
// wiping an unrelated skill on a typo. Returns nil when the archive omits
// `name` (no claim) or when the names agree.
func verifyPackageNameMatches(archivePath, positionalName string) error {
	f, openErr := os.Open(archivePath) //nolint:gosec // user-supplied path opened on user's behalf
	if openErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot open %s: %s", archivePath, openErr),
			"verify the file is readable",
		)
	}
	defer f.Close()
	info, statErr := f.Stat()
	if statErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot stat %s: %s", archivePath, statErr),
			"verify the file is readable",
		)
	}
	archiveName, peekErr := skill_api.PeekArchiveSkillName(f, info.Size())
	if peekErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot inspect %s: %s", archivePath, peekErr),
			"ensure the archive is a valid .zip containing a SKILL.md file",
		)
	}
	if archiveName == "" || archiveName == positionalName {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeInvalidSkillFile,
		fmt.Sprintf("--force refused: archive declares name %q which does not match positional argument %q", archiveName, positionalName),
		"re-run without --force, or fix the positional name / archive so they agree",
	)
}

type createMode int

const (
	modeNone createMode = iota
	modeInline
	modeFileMd
	modeFilePackage
	modeFileDirectory
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
		// Detect directories before extension matching so callers can point
		// --file at the directory `azd ai skill download` extracted (which
		// has no file extension at all).
		info, statErr := os.Stat(f.file)
		if statErr == nil && info.IsDir() {
			return modeFileDirectory, nil
		}
		ext := strings.ToLower(filepath.Ext(f.file))
		switch ext {
		case ".md":
			return modeFileMd, nil
		case ".zip":
			return modeFilePackage, nil
		}
		// A --file value with no extension can only be a directory; if
		// stat failed (typically fs.ErrNotExist), surface the stat error
		// so users aren't told the extension is unsupported when the path
		// is simply missing or unreadable.
		if statErr != nil && ext == "" {
			return modeNone, exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				fmt.Sprintf("inspect --file %q: %s", f.file, statErr),
				"verify the path exists and points to a SKILL.md, a .zip, "+
					"or a directory containing SKILL.md",
			)
		}
		return modeNone, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("unsupported --file extension %q", ext),
			"use .md for inline metadata, .zip for a package upload, "+
				"or a directory containing SKILL.md",
		)
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
		resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:     "Skill description",
				HelpMessage: "Short human-readable summary. Sent as inline_content.description on the new version.",
				Required:    true,
			},
		})
		if err != nil {
			return err
		}
		f.description = resp.Value
		f.descriptionSet = true
	}

	if strings.TrimSpace(f.instructions) == "" {
		resp, err := azdClient.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:     "Skill instructions (Markdown body)",
				HelpMessage: "Markdown body that defines the skill's behavior. Sent as inline_content.instructions.",
				Required:    true,
			},
		})
		if err != nil {
			return err
		}
		f.instructions = resp.Value
		f.instructionsSet = true
	}
	return nil
}

func newCreateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &createFlags{}
	action := &createAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new Foundry skill.",
		Long: `Create a new Foundry skill in one of four mutually exclusive modes:

  1. Inline:    --description "..." --instructions "..."
  2. SKILL.md:  --file ./SKILL.md   (CLI parses YAML front matter + body)
  3. Package:   --file ./skill.zip  (CLI uploads the archive as multipart/form-data)
  4. Directory: --file ./skill-src  (CLI packages the directory as a zip and uploads it)

Directory mode requires SKILL.md at the root of the directory — the same
layout that ` + "`azd ai skill download`" + ` writes by default, so the natural
round-trip works without a manual zip step.

Skills are versioned. ` + "`create`" + ` creates a new skill (if it does not exist)
and uploads its first version as the default. Pass --force to delete an existing
skill of the same name before creating.`,
		Example: `  azd ai skill create greet-user --description "Welcomes a new user" --instructions "Greet ..."
  azd ai skill create greet-user --file ./SKILL.md
  azd ai skill create greet-user --file ./skill.zip --force
  azd ai skill create greet-user --file ./skill-src/ --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt
			flags.descriptionSet = cmd.Flags().Changed("description")
			flags.instructionsSet = cmd.Flags().Changed("instructions")
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	cmd.Flags().StringVar(&flags.description, "description", "", "Inline mode: human-readable summary of the skill")
	cmd.Flags().StringVar(&flags.instructions, "instructions", "", "Inline mode: Markdown body defining skill behavior")
	cmd.Flags().StringVar(&flags.file, "file", "",
		"Path to SKILL.md (.md), a ZIP package (.zip), or a directory containing SKILL.md at its root")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Delete an existing skill of the same name before creating")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputJSON,
	})
	return cmd
}

// readFileWithLimit reads up to 1 MiB from path. Skill files are small in
// practice; the cap guards against reading a giant file by accident.
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
			fmt.Sprintf("%s is a directory; expected a skill file", path),
			"pass a single file",
		)
	}
	const maxBytes = 1 << 20
	if info.Size() > maxBytes {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("%s exceeds the 1 MiB skill file size limit (got %d bytes)", path, info.Size()),
			"split the file into smaller assets and use a package upload",
		)
	}
	data, err := os.ReadFile(path) //nolint:gosec // user-supplied path read on user's behalf
	if err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("cannot read %s: %s", path, err),
			"verify the file is readable",
		)
	}
	return data, nil
}

func shouldSuppressWarning(noPrompt bool, format string) bool {
	return noPrompt || format == outputJSON
}

func isNotFound(err error) bool {
	if respErr, ok := errors.AsType[*azcore.ResponseError](err); ok {
		return respErr.StatusCode == 404
	}
	return false
}
