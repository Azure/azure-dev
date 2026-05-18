// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"azureaiskills/internal/exterrors"
	"azureaiskills/internal/pkg/skill_api"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// downloadFlags holds parsed input for the `skill download` command.
type downloadFlags struct {
	name            string
	outputDir       string
	raw             bool
	force           bool
	output          string
	projectEndpoint string

	outputDirSet bool
}

// downloadAction is the download-command implementation.
type downloadAction struct {
	flags *downloadFlags
}

// downloadResult is the JSON shape printed when --output=json. The shape is
// part of the published contract: callers depend on it.
type downloadResult struct {
	Skill     string   `json:"skill"`
	OutputDir string   `json:"outputDir"`
	Files     []string `json:"files,omitempty"`
	Archive   string   `json:"archive,omitempty"`
	Raw       bool     `json:"raw"`
}

// Run executes the download operation.
func (a *downloadAction) Run(ctx context.Context) error {
	if err := validateSkillName(a.flags.name); err != nil {
		return err
	}

	outputDir := a.flags.outputDir
	if outputDir == "" {
		outputDir = filepath.Join(".agents", "skills", a.flags.name)
	}
	absOut, err := filepath.Abs(outputDir)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid --output-dir: %s", err),
			"pass a valid filesystem path",
		)
	}

	skillCtx, err := resolveSkillContext(ctx, a.flags.projectEndpoint)
	if err != nil {
		return err
	}

	// Pre-flight via Get so we can detect the "no associated package" case
	// before issuing the :download call. The service returns 404 with a
	// dedicated error code when the skill was created from inline JSON (or a
	// SKILL.md file) rather than a ZIP package, but the message is opaque —
	// surfacing the HasBlob check up front gives the user a clearer answer.
	skill, err := skillCtx.client.Get(ctx, a.flags.name)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpGetSkill)
	}
	if !skill.HasBlob {
		return exterrors.Validation(
			exterrors.CodeSkillNoPackage,
			fmt.Sprintf("skill %q has no downloadable package", a.flags.name),
			"only skills created from a `.zip` archive have a downloadable "+
				"package. Use `azd ai skill show <name>` to inspect metadata; "+
				"re-create with `azd ai skill create <name> --file <archive>.zip --force` "+
				"if you want a downloadable copy.",
		)
	}

	body, err := skillCtx.client.Download(ctx, a.flags.name)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpDownloadSkill)
	}

	if a.flags.raw {
		return a.writeRaw(body, absOut)
	}
	return a.writeExtracted(body, absOut)
}

func (a *downloadAction) writeRaw(body []byte, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	archivePath := filepath.Join(outputDir, a.flags.name+".zip")

	// Always Lstat (even with --force) so we never follow a symlink and so we
	// refuse to overwrite a non-regular file (directory, device, socket, ...).
	if statInfo, statErr := os.Lstat(archivePath); statErr == nil {
		if statInfo.Mode()&os.ModeSymlink != 0 {
			return exterrors.Validation(
				exterrors.CodeSkillOutputCollision,
				fmt.Sprintf("%s is a symlink; refusing to follow", archivePath),
				"remove the symlink and re-run",
			)
		}
		if !statInfo.Mode().IsRegular() {
			return exterrors.Validation(
				exterrors.CodeSkillOutputCollision,
				fmt.Sprintf("%s exists and is not a regular file", archivePath),
				"remove or rename the existing entry and re-run",
			)
		}
		if !a.flags.force {
			return exterrors.Validation(
				exterrors.CodeSkillOutputCollision,
				fmt.Sprintf("%s already exists", archivePath),
				"pass --force to overwrite",
			)
		}
		// --force: remove the existing regular file so the subsequent O_EXCL
		// open creates a fresh file owned by this process. This avoids any
		// TOCTOU window where the path could be swapped for a symlink between
		// the Lstat and the open.
		if rmErr := os.Remove(archivePath); rmErr != nil {
			return fmt.Errorf("remove existing archive: %w", rmErr)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", archivePath, statErr)
	}

	// O_EXCL guarantees we create the file ourselves; if anything appeared
	// at archivePath in the meantime (e.g. a freshly-planted symlink), the
	// open fails rather than silently following it.
	//nolint:gosec // archivePath is built from user-supplied --output-dir + skill name, written on user behalf
	f, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	if _, copyErr := f.Write(body); copyErr != nil {
		_ = f.Close()
		return fmt.Errorf("write archive: %w", copyErr)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}

	res := downloadResult{
		Skill:     a.flags.name,
		OutputDir: outputDir,
		Archive:   a.flags.name + ".zip",
		Raw:       true,
	}
	return a.printResult(res)
}

func (a *downloadAction) writeExtracted(body []byte, outputDir string) error {
	result, err := skill_api.SafeExtract(body, skill_api.ExtractOptions{
		OutputDir: outputDir,
		Force:     a.flags.force,
	})
	if err != nil {
		return classifyExtractError(err, outputDir)
	}

	res := downloadResult{
		Skill:     a.flags.name,
		OutputDir: outputDir,
		Files:     result.Files,
		Raw:       false,
	}
	return a.printResult(res)
}

func (a *downloadAction) printResult(res downloadResult) error {
	if a.flags.output == outputJSON {
		return printJSON(res)
	}
	if res.Raw {
		fmt.Printf("Skill %q downloaded to %s\n", res.Skill, filepath.Join(res.OutputDir, res.Archive))
	} else {
		fmt.Printf("Skill %q extracted into %s (%d files)\n", res.Skill, res.OutputDir, len(res.Files))
		for _, name := range res.Files {
			fmt.Printf("  %s\n", name)
		}
	}
	return nil
}

// classifyExtractError converts a SafeExtract error into a structured
// extension error when possible. Unknown errors propagate unchanged so the
// gRPC layer surfaces them with a default Internal category.
func classifyExtractError(err error, outputDir string) error {
	switch {
	case errors.Is(err, skill_api.ErrUnsafeEntry):
		return exterrors.Validation(
			exterrors.CodeSkillArchiveUnsafe,
			err.Error(),
			"the downloaded archive contains an unsafe entry; do not extract it",
		)
	case errors.Is(err, skill_api.ErrLimitExceeded):
		return exterrors.Validation(
			exterrors.CodeSkillArchiveUnsafe,
			err.Error(),
			"the archive exceeds the per-skill decompression safety limit",
		)
	case errors.Is(err, skill_api.ErrCollision):
		return exterrors.Validation(
			exterrors.CodeSkillOutputCollision,
			err.Error(),
			fmt.Sprintf("pass --force to overwrite existing files in %s", outputDir),
		)
	case errors.Is(err, skill_api.ErrInvalidZip):
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			err.Error(),
			"the service did not return a valid zip archive; retry or contact support",
		)
	}
	return err
}

// newDownloadCommand constructs the `skill download` Cobra command.
func newDownloadCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &downloadFlags{}
	action := &downloadAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "download <name>",
		Short: "Download a Foundry skill package.",
		Long: `Download a skill's ZIP package.

By default the CLI extracts the archive into --output-dir (which defaults to
'./.agents/skills/<name>/'). Pass --raw to write the unmodified ZIP archive
into --output-dir instead.

Extraction enforces strict safety rules: no absolute paths, no '..' segments,
no symlinks / non-regular entries, and a 10,000-entry / 512 MB cap on the
total uncompressed size.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.outputDirSet = cmd.Flags().Changed("output-dir")
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")

			ctx := azdext.WithAccessToken(cmd.Context())
			return action.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&flags.outputDir, "output-dir", "",
		"Directory to write the extracted skill (default: ./.agents/skills/<name>/)")
	cmd.Flags().BoolVar(&flags.raw, "raw", false,
		"Skip extraction; write the ZIP archive as-is to --output-dir")
	cmd.Flags().BoolVar(&flags.force, "force", false,
		"Overwrite existing files in --output-dir")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputTable,
	})
	return cmd
}
