// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/otiai10/copy"
)

// "legacy" in this case covers our existing Aspire code path, and the old 'resources' based code path.
// These both use built-in infra-generation and not the newer extensible version that we provide for
// extensions.

// writeLegacyGeneratedFiles writes legacy-generated files in synthFS to target. Unless --force is set, existing files
// that would be overwritten are listed and the user chooses to overwrite or keep them. In --no-prompt mode, any
// such file is an error.
func (a *infraGenerateAction) writeLegacyGeneratedFiles(ctx context.Context, target string, synthFS fs.FS) error {
	staging, err := os.MkdirTemp("", "infra-generate")
	if err != nil {
		return fmt.Errorf("creating infrastructure staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staging); err != nil {
			log.Printf("removing infrastructure staging directory: %v", err)
		}
	}()

	// Stage all generated files before touching the project, so a read failure cannot leave it partially updated.
	err = fs.WalkDir(synthFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		contents, err := fs.ReadFile(synthFS, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(staging, filepath.Dir(path)), osutil.PermissionDirectory); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(staging, path), contents, osutil.PermissionFile)
	})
	if err != nil {
		return fmt.Errorf("staging generated infrastructure: %w", err)
	}

	var keep map[string]struct{}
	if !a.flags.force {
		keep, err = a.promptForExistingLegacyFiles(ctx, staging, target)
		if err != nil {
			return err
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	options := copy.Options{
		Skip: func(_ os.FileInfo, src, _ string) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			_, skip := keep[src]
			return skip, nil
		},
	}
	if err := copy.Copy(staging, target, options); err != nil {
		return fmt.Errorf("copying contents from temp staging directory: %w", err)
	}

	return nil
}

// promptForExistingLegacyFiles checks staged files against target and returns the staged paths to keep unchanged.
func (a *infraGenerateAction) promptForExistingLegacyFiles(
	ctx context.Context, staging string, target string,
) (map[string]struct{}, error) {
	var existing []string

	// Legacy generated files aren't confined to infra.path (for example, Aspire writes manifests next to each
	// service), so every generated file is checked individually.
	if err := filepath.WalkDir(staging, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}

		name, err := filepath.Rel(staging, path)
		if err != nil {
			return err
		}
		_, err = os.Lstat(filepath.Join(target, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}

		existing = append(existing, name)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("checking for existing infrastructure: %w", err)
	}

	if len(existing) == 0 {
		return nil, nil
	}

	if a.console.IsNoPromptMode() {
		return nil, &internal.ErrorWithSuggestion{
			Err:        fmt.Errorf("generated files already exist: %s", strings.Join(existing, ", ")),
			Message:    "Generated infrastructure would overwrite existing files.",
			Suggestion: "Rerun with --force to overwrite the existing files.",
		}
	}

	a.console.MessageUxItem(ctx, &ux.WarningMessage{
		Description: "The following files would be overwritten by generated versions:",
	})

	for _, file := range existing {
		a.console.Message(ctx, fmt.Sprintf(" * %s", file))
	}

	selection, err := a.console.Select(ctx, input.ConsoleOptions{
		Message: "What would you like to do with these files?",
		Options: []string{
			"Overwrite with the generated versions",
			"Keep my existing files unchanged",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("prompting to overwrite: %w", err)
	}

	if selection == 0 {
		return nil, nil
	}

	keep := make(map[string]struct{}, len(existing))
	for _, file := range existing {
		keep[filepath.Join(staging, file)] = struct{}{}
	}

	return keep, nil
}
