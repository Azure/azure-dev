// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
	saveToAzureYaml bool
	output          string
	projectEndpoint string

	descriptionSet  bool
	instructionsSet bool
}

type updateAction struct{ flags *updateFlags }

func (a *updateAction) saveService(
	ctx context.Context,
	cfg skillServiceConfig,
	archiveSource string,
) error {
	if !a.flags.saveToAzureYaml {
		return nil
	}
	return saveSkillServiceToProject(ctx, skillServiceDeclaration{
		Name:          a.flags.name,
		Config:        cfg,
		ArchiveSource: archiveSource,
	})
}

// updateMode is the dispatch tag selectUpdateMode returns. It mirrors
// createMode so the two commands route the same shapes through the same
// helpers (md/zip/dir) — the difference is that `update` always appends a
// non-destructive new version via POST /skills/{name}/versions and never
// deletes the existing skill.
type updateMode int

const (
	updateModeNone updateMode = iota
	updateModeSetDefault
	updateModeInline
	updateModeFileMd
	updateModeFilePackage
	updateModeFileDirectory
)

func (a *updateAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}
	mode, err := selectUpdateMode(a.flags)
	if err != nil {
		return err
	}

	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}

	switch mode {
	case updateModeSetDefault:
		return a.runSetDefault(ctx, skillCtx.client)
	case updateModeInline, updateModeFileMd:
		return a.runInline(ctx, skillCtx.client)
	case updateModeFilePackage:
		return a.runFilePackage(ctx, skillCtx.client)
	case updateModeFileDirectory:
		return a.runFileDirectory(ctx, skillCtx.client)
	}
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"unsupported update mode",
		"this is a bug; please file an issue",
	)
}

// runSetDefault is a metadata-only update: POST /skills/{name} with
// { default_version }. No new version is created.
func (a *updateAction) runSetDefault(ctx context.Context, client *skill_api.Client) error {
	updated, err := client.UpdateSkillDefaultVersion(ctx, a.flags.name, a.flags.setDefault)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateSkill)
	}
	if a.flags.output == outputJSON {
		return printJSON(updated)
	}
	fmt.Printf("Skill %q default_version set to %q.\n", updated.Name, updated.DefaultVersion)
	return printSkillDetail(updated, outputTable)
}

// runInline handles both pure-inline (--description/--instructions) and
// SKILL.md (--file *.md) updates: parse content, POST inline_content JSON.
func (a *updateAction) runInline(ctx context.Context, client *skill_api.Client) error {
	content, err := a.buildInlineContent()
	if err != nil {
		return err
	}

	version, err := client.CreateVersionInline(ctx, a.flags.name, skill_api.CreateVersionRequest{
		InlineContent: content,
		Default:       true,
	})
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateSkill)
	}
	if err := a.saveService(ctx, skillServiceConfig{
		Description:  content.Description,
		Instructions: content.Instructions,
		Tools:        content.AllowedTools,
	}, ""); err != nil {
		return err
	}
	return a.printUpdateResult(ctx, client, version)
}

// runFilePackage uploads a single .zip archive as the next default version
// via multipart/form-data. Mirrors createAction.runFilePackage but wraps
// errors with OpUpdateSkill and uses printUpdateResult.
func (a *updateAction) runFilePackage(ctx context.Context, client *skill_api.Client) error {
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
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateSkill)
	}
	if err := a.saveService(ctx, skillServiceConfig{}, a.flags.file); err != nil {
		return err
	}
	return a.printUpdateResult(ctx, client, version)
}

// runFileDirectory packages the directory as an in-memory zip and uploads
// it via the same multipart path as runFilePackage. The directory must
// contain a SKILL.md at its root (matching what `azd ai skill download`
// writes by default), so the natural download → edit → update flow works
// without any manual zip step. Mirrors createAction.runFileDirectory but
// wraps errors with OpUpdateSkill and uses printUpdateResult.
func (a *updateAction) runFileDirectory(ctx context.Context, client *skill_api.Client) error {
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
		return exterrors.ServiceFromAzure(err, exterrors.OpUpdateSkill)
	}
	if err := a.saveService(ctx, skillServiceConfig{}, a.flags.file); err != nil {
		return err
	}
	return a.printUpdateResult(ctx, client, version)
}

// printUpdateResult prints either the created version envelope (JSON) or,
// for human output, a friendly "updated" message followed by the freshly
// loaded Skill so users see the new default_version / latest_version.
//
// Critically, when --output json is in effect we ONLY emit the version
// envelope — never a human-readable line — so the output stays valid JSON
// for callers piping into jq or similar.
func (a *updateAction) printUpdateResult(ctx context.Context, client *skill_api.Client, version *skill_api.SkillVersion) error {
	if a.flags.output == outputJSON {
		return printJSON(version)
	}
	fmt.Printf("Skill %q updated; new version %q is now the default.\n", a.flags.name, version.Version)
	skill, err := client.GetSkill(ctx, a.flags.name)
	if err != nil {
		// Don't fail the update just because the follow-up GET failed; fall
		// back to printing the version envelope instead.
		fmt.Fprintf(os.Stderr, "Warning: could not fetch skill details: %v\n", err)
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
		content.License = parsed.License
		content.Compatibility = parsed.Compatibility
		content.AllowedTools = parsed.AllowedTools
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

// validateFlags is kept for tests that exercise validation in isolation. It
// delegates to selectUpdateMode (which performs the same checks plus mode
// inference) and discards the mode. Production code calls selectUpdateMode
// directly via Run.
func (a *updateAction) validateFlags() error {
	_, err := selectUpdateMode(a.flags)
	return err
}

// selectUpdateMode validates flag combinations and infers the dispatch
// mode. It mirrors selectCreateMode in skill_create.go so the same input
// shapes (.md / .zip / directory / inline) route the same way on update,
// with the additional --set-default-version branch that is unique to
// update.
func selectUpdateMode(f *updateFlags) (updateMode, error) {
	inlineProvided := f.descriptionSet || f.instructionsSet
	fileProvided := f.file != ""
	setDefaultProvided := f.setDefault != ""

	if setDefaultProvided && f.saveToAzureYaml {
		return updateModeNone, exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--save-to-azure-yaml cannot be combined with --set-default-version",
			"omit --save-to-azure-yaml, or provide new inline/file content that azure.yaml can reconcile",
		)
	}

	// --set-default-version is a metadata-only update; it cannot be combined
	// with content flags. Hand it back as its own mode so Run can dispatch
	// the POST /skills/{name} envelope instead of POST /versions.
	if setDefaultProvided && (inlineProvided || fileProvided) {
		return updateModeNone, exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--set-default-version cannot be combined with --description / --instructions / --file",
			"pass --set-default-version on its own, or omit it to create a new default version",
		)
	}
	if setDefaultProvided {
		return updateModeSetDefault, nil
	}

	if !inlineProvided && !fileProvided {
		return updateModeNone, exterrors.Validation(
			exterrors.CodeMissingRequiredField,
			"no fields to update",
			"pass --description, --instructions, and/or --file <path>; or use --set-default-version <ver>",
		)
	}
	if inlineProvided && fileProvided {
		return updateModeNone, exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"--file is mutually exclusive with --description / --instructions on update",
			"pass either inline flags or --file <path>, not both",
		)
	}

	if fileProvided {
		// Detect directories before extension matching so callers can point
		// --file at the directory `azd ai skill download` extracted (which
		// has no file extension at all). Matches selectCreateMode.
		info, statErr := os.Stat(f.file)
		if statErr == nil && info.IsDir() {
			return updateModeFileDirectory, nil
		}
		ext := strings.ToLower(filepath.Ext(f.file))
		switch ext {
		case ".md":
			return updateModeFileMd, nil
		case ".zip":
			return updateModeFilePackage, nil
		}
		// A --file value with no extension can only be a directory; if
		// stat failed (typically fs.ErrNotExist), surface the stat error
		// so users aren't told the extension is unsupported when the path
		// is simply missing or unreadable.
		if statErr != nil && ext == "" {
			return updateModeNone, exterrors.Validation(
				exterrors.CodeInvalidSkillFile,
				fmt.Sprintf("inspect --file %q: %s", f.file, statErr),
				"verify the path exists and points to a SKILL.md, a .zip, "+
					"or a directory containing SKILL.md",
			)
		}
		return updateModeNone, exterrors.Validation(
			exterrors.CodeInvalidSkillFile,
			fmt.Sprintf("unsupported --file extension %q on update", ext),
			"use .md for inline metadata, .zip for a package upload, "+
				"or a directory containing SKILL.md",
		)
	}

	return updateModeInline, nil
}

func newUpdateCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &updateFlags{}
	action := &updateAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Create a new default version for a Foundry skill.",
		Long: `Skills are versioned and immutable. ` + "`update`" + ` creates a new version and
sets it as the skill's new default version. ` + "`update`" + ` is non-destructive:
prior versions are preserved and remain reachable.

Accepts the same four input shapes as ` + "`create`" + `:

  1. Inline:    --description "..." --instructions "..."
  2. SKILL.md:  --file ./SKILL.md   (CLI parses YAML front matter + body)
  3. Package:   --file ./skill.zip  (CLI uploads the archive as multipart/form-data)
  4. Directory: --file ./skill-src  (CLI packages the directory as a zip and uploads it)

Directory mode requires SKILL.md at the root of the directory — the same
layout that ` + "`azd ai skill download`" + ` writes by default.

To repoint default_version at an existing version without uploading new
content, pass --set-default-version <version>.

Pass --save-to-azure-yaml to add or update a host: azure.ai.skill service in
the current azd project's azure.yaml. Inline and SKILL.md inputs are saved as
inline service properties; ZIP and directory inputs are saved as portable
archive references for azd deploy/up to reconcile.`,
		Example: `  azd ai skill update my-skill --description "Updated summary" --instructions "..."
  azd ai skill update my-skill --file ./SKILL.md
  azd ai skill update my-skill --file ./SKILL.md --save-to-azure-yaml
  azd ai skill update my-skill --file ./skill.zip
  azd ai skill update my-skill --file ./skill-src/
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
	cmd.Flags().StringVar(&flags.file, "file", "",
		"Path to a SKILL.md file, a .zip archive, or a directory whose contents become the next version. "+
			"Archives and directories must contain a SKILL.md at the root.")
	cmd.Flags().StringVar(&flags.setDefault, "set-default-version", "", "Set the skill's default_version to an existing version without uploading new content")
	cmd.Flags().BoolVar(
		&flags.saveToAzureYaml,
		"save-to-azure-yaml",
		false,
		"Add or update this skill as a host: azure.ai.skill service in the current azure.yaml",
	)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputJSON,
	})
	return cmd
}
