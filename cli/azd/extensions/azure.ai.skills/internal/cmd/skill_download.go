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

type downloadFlags struct {
	name            string
	version         string
	outputDir       string
	raw             bool
	force           bool
	output          string
	projectEndpoint string

	outputDirSet bool
}

type downloadAction struct{ flags *downloadFlags }

// downloadResult is the JSON shape printed when --output=json. Public contract.
type downloadResult struct {
	Skill     string   `json:"skill"`
	Version   string   `json:"version,omitempty"`
	OutputDir string   `json:"outputDir"`
	Files     []string `json:"files,omitempty"`
	Archive   string   `json:"archive,omitempty"`
	Raw       bool     `json:"raw"`
}

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

	var body []byte
	if a.flags.version != "" {
		body, err = skillCtx.client.DownloadVersionContent(ctx, a.flags.name, a.flags.version)
	} else {
		body, err = skillCtx.client.DownloadSkillContent(ctx, a.flags.name)
	}
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

	archiveName := a.flags.name + ".zip"
	archivePath := filepath.Join(outputDir, archiveName)

	// Always Lstat (even with --force) so we never follow a symlink and so we
	// refuse to overwrite a non-regular file.
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
		// Remove first so the subsequent O_EXCL open is atomic — closes the
		// TOCTOU window where the path could be swapped for a symlink.
		if rmErr := os.Remove(archivePath); rmErr != nil {
			return fmt.Errorf("remove existing archive: %w", rmErr)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", archivePath, statErr)
	}

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

	return a.printResult(downloadResult{
		Skill: a.flags.name, Version: a.flags.version, OutputDir: outputDir, Archive: archiveName, Raw: true,
	})
}

func (a *downloadAction) writeExtracted(body []byte, outputDir string) error {
	result, err := skill_api.SafeExtract(body, skill_api.ExtractOptions{
		OutputDir: outputDir,
		Force:     a.flags.force,
	})
	if err != nil {
		return classifyExtractError(err, outputDir)
	}
	return a.printResult(downloadResult{
		Skill: a.flags.name, Version: a.flags.version, OutputDir: outputDir, Files: result.Files,
	})
}

func (a *downloadAction) printResult(res downloadResult) error {
	if a.flags.output == outputJSON {
		return printJSON(res)
	}
	versionSuffix := ""
	if res.Version != "" {
		versionSuffix = fmt.Sprintf(" (version %s)", res.Version)
	}
	if res.Raw {
		fmt.Printf("Skill %q%s downloaded to %s\n", res.Skill, versionSuffix, filepath.Join(res.OutputDir, res.Archive))
	} else {
		fmt.Printf("Skill %q%s extracted into %s (%d files)\n", res.Skill, versionSuffix, res.OutputDir, len(res.Files))
		for _, name := range res.Files {
			fmt.Printf("  %s\n", name)
		}
	}
	return nil
}

// classifyExtractError wraps SafeExtract sentinels in structured extension
// errors. Unknown errors propagate as-is.
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
	case errors.Is(err, skill_api.ErrInvalidArchive):
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			err.Error(),
			"the service did not return a recognizable ZIP archive; retry or contact support",
		)
	}
	return err
}

func newDownloadCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	flags := &downloadFlags{}
	action := &downloadAction{flags: flags}

	cmd := &cobra.Command{
		Use:   "download <name>",
		Short: "Download a Foundry skill package.",
		Long: `Download the zip content for a skill and extract it into --output-dir
(default ` + "`./.agents/skills/<name>/`" + `). Pass --raw to write the unmodified
zip archive instead of extracting it. Pass --version <ver> to download a
specific version rather than the skill's default.

Extraction enforces strict safety rules: no absolute paths, no '..' segments,
no symlinks / non-regular entries, and a 10,000-entry / 512 MB cap on the
total uncompressed size.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.name = args[0]
			flags.output = extCtx.OutputFormat
			flags.outputDirSet = cmd.Flags().Changed("output-dir")
			flags.projectEndpoint, _ = cmd.Flags().GetString("project-endpoint")
			return action.Run(azdext.WithAccessToken(cmd.Context()))
		},
	}

	cmd.Flags().StringVar(&flags.version, "version", "", "Download a specific version (defaults to the skill's default_version)")
	cmd.Flags().StringVar(&flags.outputDir, "output-dir", "", "Directory to write the extracted skill (default: ./.agents/skills/<name>/)")
	cmd.Flags().BoolVar(&flags.raw, "raw", false, "Skip extraction; write the zip archive as-is to --output-dir")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Overwrite existing files in --output-dir")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name: "output", AllowedValues: []string{outputJSON, outputTable}, Default: outputTable,
	})
	return cmd
}
