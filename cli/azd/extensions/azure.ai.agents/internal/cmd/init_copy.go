// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

const (
	// copyConfirmThreshold is the max file/folder count before prompting for confirmation.
	copyConfirmThreshold = 10
	// previewLimit is the max items shown in the directory preview.
	previewLimit = 5
)

// validateLocalContainerAgentCopy checks if copying the manifest directory to targetDir is safe,
// prompting for confirmation if the directory contains many files.
func (a *InitAction) validateLocalContainerAgentCopy(ctx context.Context, manifestPointer string, targetDir string) error {
	manifestDir := filepath.Dir(manifestPointer)
	srcAbs, err := filepath.Abs(manifestDir)
	if err != nil {
		return fmt.Errorf("resolving manifest directory %s: %w", manifestDir, err)
	}
	dstAbs, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolving target directory %s: %w", targetDir, err)
	}

	// Re-init case: manifest already lives in the destination directory.
	// We still overwrite agent.yaml, but we should not attempt to copy the directory into itself.
	if isSamePath(dstAbs, srcAbs) {
		return nil
	}

	if isSubpath(dstAbs, srcAbs) {
		return fmt.Errorf(
			"destination '%s' is inside the agent manifest directory '%s'. "+
				"Move the manifest to a separate directory to avoid copying into itself",
			dstAbs,
			srcAbs,
		)
	}

	entries, err := os.ReadDir(srcAbs)
	if err != nil {
		return fmt.Errorf("reading manifest directory %s: %w", srcAbs, err)
	}
	entryCount := len(entries)
	if entryCount <= copyConfirmThreshold {
		return nil
	}

	if a.flags.NoPrompt {
		return nil
	}

	preview, err := formatDirectoryPreview(entries, previewLimit)
	if err != nil {
		return fmt.Errorf("enumerating files and folders in %s: %w", srcAbs, err)
	}

	fmt.Printf("%s", output.WithWarningFormat(
		"\nThe agent manifest directory '%s' contains %d files and folders that will be copied into '%s': %s\n\n",
		srcAbs,
		entryCount,
		dstAbs,
		preview))

	confirmResponse, err := a.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message:      "Continue?",
			DefaultValue: to.Ptr(false),
			HelpMessage:  "To avoid copying too much, place the manifest in a dedicated folder with only the agent files you want to include.",
		},
	})
	if err != nil {
		return fmt.Errorf("prompting for confirmation: %w", err)
	}
	if confirmResponse == nil || confirmResponse.Value == nil || !*confirmResponse.Value {
		return fmt.Errorf("operation cancelled by user")
	}

	return nil
}

// formatDirectoryPreview returns a comma-separated preview of directory entries,
// truncating with "(+N more)" if exceeding maxEntries.
func formatDirectoryPreview(entries []os.DirEntry, maxEntries int) (string, error) {
	labels := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		labels = append(labels, name)
	}

	slices.Sort(labels)
	if maxEntries <= 0 || len(labels) <= maxEntries {
		return strings.Join(labels, ", "), nil
	}

	return fmt.Sprintf("%s, ... (+%d more)", strings.Join(labels[:maxEntries], ", "), len(labels)-maxEntries), nil
}

// isSubpath returns true if child is inside or equal to parent.
func isSubpath(child, parent string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isSamePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// copyDirectory recursively copies all files and directories from src to dst.
func copyDirectory(src, dst string) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("resolving absolute source path %s: %w", src, err)
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("resolving absolute destination path %s: %w", dst, err)
	}

	// No-op: already in the destination directory (re-init / overwrite scenario).
	if isSamePath(dstAbs, srcAbs) {
		return nil
	}

	if isSubpath(dstAbs, srcAbs) {
		return fmt.Errorf("refusing to copy directory '%s' into its own subtree '%s'", srcAbs, dstAbs)
	}

	return filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate the destination path
		relPath, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dstAbs, relPath)

		if d.IsDir() {
			// Create directory and continue processing its contents
			return os.MkdirAll(dstPath, 0755)
		}

		// Copy file
		return copyFile(path, dstPath)
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	// Create the destination directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// Create destination file
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	// Copy file contents
	_, err = srcFile.WriteTo(dstFile)
	return err
}
