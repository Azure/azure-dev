// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"azureaidataset/internal/messages"
	"azureaidataset/internal/pkg/dataset_api"

	"github.com/spf13/cobra"
)

// datasetDownloadAction writes a registered dataset version to disk.
type datasetDownloadAction struct {
	cmd       *cobra.Command
	endpoint  string
	name      string
	version   string
	outputDir string
	outFile   string
	force     bool
}

func newDatasetDownloadCommand() *cobra.Command {
	a := &datasetDownloadAction{}

	cmd := &cobra.Command{
		Use:   "download <name>",
		Short: "Download a registered dataset version's content.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.cmd, a.name = cmd, args[0]
			return a.Run()
		},
	}

	cmd.Flags().StringVar(&a.version, "version", "",
		"Version to download. Omit for the latest, which is reported.")
	cmd.Flags().StringVar(&a.outputDir, "output-dir", "",
		"Directory to write into. Defaults to the current directory.")
	cmd.Flags().StringVar(&a.outFile, "output-file", "",
		"Exact path to write. Only valid for a single-file dataset.")
	cmd.Flags().BoolVar(&a.force, "force", false,
		"Overwrite files that already exist.")
	cmd.Flags().StringVar(&a.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	registerOutputFormats(cmd)
	return cmd
}

func (a *datasetDownloadAction) Run() error {
	if !validAssetName(a.name) {
		return messages.InvalidDatasetName(a.name)
	}
	// Refused before the round trip: both name a destination, and honouring
	// either one silently would write somewhere the caller did not ask for.
	if a.outFile != "" && a.outputDir != "" {
		return messages.OutputFileAndDirBothGiven()
	}

	ctx := a.cmd.Context()
	ec, err := newDatasetContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	version := a.version
	if version == "" {
		if version, err = latestVersionForShow(ctx, ec.datasetClient, a.name); err != nil {
			return err
		}
	}

	content, err := ec.datasetClient.ListDatasetContent(
		ctx, a.name, version, ProjectEndpointAPIVersion)
	if err != nil {
		return messages.DownloadingDataset(a.name, version, err)
	}
	// A folder dataset has no single path to be written to, and picking one of
	// its files to satisfy the flag would hand back part of the dataset under a
	// name that claims to be all of it.
	if a.outFile != "" && !content.SingleFile {
		return messages.OutputFileNeedsSingleFileDataset(a.name, version, len(content.Files))
	}

	written, path, err := a.write(ctx, ec, content, version)
	if err != nil {
		return err
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), map[string]any{
			"dataset":    a.name,
			"version":    version,
			"path":       path,
			"files":      written,
			"singleFile": content.SingleFile,
		})
	}
	out := a.cmd.OutOrStdout()
	fmt.Fprint(out, messages.DownloadedDataset(
		a.name, version, content.SingleFile, written, path))
	if prefix := ec.portalPrefix(ctx); prefix != nil {
		writePortalLink(out, prefix.DatasetURL(a.name, version))
	}
	return nil
}

// write puts the content on disk and reports how many files and where.
//
// Nothing lands at the destination until every file has arrived. A download
// interrupted halfway used to leave a directory that looks like a dataset and
// is short of rows, which is the one failure a later run cannot detect.
func (a *datasetDownloadAction) write(
	ctx context.Context,
	ec *datasetContext,
	content *dataset_api.DatasetContent,
	version string,
) (int, string, error) {
	dir := a.outputDir
	if dir == "" {
		dir = "."
	}

	if content.SingleFile {
		dest := a.outFile
		if dest == "" {
			leaf, err := derivedLeafName(a.name, version, content.Extension())
			if err != nil {
				return 0, "", err
			}
			dest = filepath.Join(dir, leaf)
		}
		if err := refuseExisting(dest, a.force); err != nil {
			return 0, "", err
		}
		body, err := ec.datasetClient.Open(ctx, content, content.Files[0])
		if err != nil {
			return 0, "", messages.DownloadingDataset(a.name, version, err)
		}
		defer body.Close()
		if err := writeFileAtomically(dest, body); err != nil {
			return 0, "", err
		}
		return 1, dest, nil
	}

	leaf, err := derivedLeafName(a.name, version, "")
	if err != nil {
		return 0, "", err
	}
	dest := filepath.Join(dir, leaf)
	if err := refuseExisting(dest, a.force); err != nil {
		return 0, "", err
	}

	// The single-file branch creates missing parents, so this one has to as
	// well: --output-dir does not promise the directory already exists, and
	// MkdirTemp fails outright when its parent does not.
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return 0, "", messages.CreatingDirectory(filepath.Dir(dest), err)
	}

	// Staged beside the destination rather than in the system temp directory:
	// the rename at the end is only atomic within one filesystem, and a temp
	// directory on another volume turns it into a copy that can fail halfway.
	staging, err := os.MkdirTemp(filepath.Dir(dest), ".azd-dataset-*")
	if err != nil {
		return 0, "", messages.StagingDownload(err)
	}
	defer os.RemoveAll(staging)

	for _, file := range content.Files {
		local, err := safeJoin(staging, file)
		if err != nil {
			return 0, "", err
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o750); err != nil {
			return 0, "", messages.CreatingDirectory(filepath.Dir(local), err)
		}
		body, err := ec.datasetClient.Open(ctx, content, file)
		if err != nil {
			return 0, "", messages.DownloadingDataset(a.name, version, err)
		}
		err = writeFileAtomically(local, body)
		_ = body.Close()
		if err != nil {
			return 0, "", err
		}
	}

	// Renaming onto an existing directory fails whatever --force said, so the
	// old one moves aside first and is discarded only once the new one is in
	// place. Put back on failure: --force is not permission to lose both.
	replaced := ""
	if _, err := os.Lstat(dest); err == nil {
		replaced = dest + ".azd-replaced"
		_ = os.RemoveAll(replaced)
		if err := os.Rename(dest, replaced); err != nil {
			return 0, "", messages.WritingDownload(dest, err)
		}
	}
	if err := os.Rename(staging, dest); err != nil {
		if replaced != "" {
			_ = os.Rename(replaced, dest)
		}
		return 0, "", messages.WritingDownload(dest, err)
	}
	if replaced != "" {
		_ = os.RemoveAll(replaced)
	}
	return len(content.Files), dest, nil
}

// safeJoin resolves a service-supplied entry name under root, or refuses.
//
// The names come from a listing, so they are not this command's to trust: an
// absolute path or one climbing out with `..` would write wherever it said, and
// the destination is chosen by the caller precisely so that it does not.
func safeJoin(root, name string) (string, error) {
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", messages.DatasetEntryNotRelative(name)
	}
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return "", messages.DatasetEntryNotRelative(name)
	}
	joined := filepath.Join(root, cleaned)
	// Belt and braces: Join cleans again, so this catches anything the checks
	// above modelled differently from the filesystem.
	rel, err := filepath.Rel(root, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", messages.DatasetEntryNotRelative(name)
	}
	return joined, nil
}

// refuseExisting stops a download from replacing what is already there.
func refuseExisting(path string, force bool) error {
	if force {
		return nil
	}
	if _, err := os.Lstat(path); err == nil {
		return messages.DownloadDestinationExists(path)
	}
	return nil
}

// derivedLeafName is the file or folder a download lands in when the caller
// named none.
//
// The name is the caller's and the version is the service's, and both were
// interpolated straight into a path: `--version ../..` resolved outside the
// output directory, because filepath.Join cleans the `..` rather than refusing
// it. Neither may be anything but one path component.
func derivedLeafName(name, version, extension string) (string, error) {
	for _, part := range []string{name, version} {
		if part == "" || part == "." || part == ".." ||
			strings.ContainsAny(part, `/\`) {
			return "", messages.DownloadNameNotAPathComponent(part)
		}
	}
	return fmt.Sprintf("%s-%s%s", name, version, extension), nil
}

// writeFileAtomically writes body to path via a temporary file in the same
// directory, so a failed or interrupted write leaves nothing behind under the
// name a reader would trust.
func writeFileAtomically(path string, body io.Reader) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return messages.CreatingDirectory(dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".azd-download-*")
	if err != nil {
		return messages.WritingDownload(path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		return messages.WritingDownload(path, err)
	}
	if err := tmp.Close(); err != nil {
		return messages.WritingDownload(path, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return messages.WritingDownload(path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return messages.WritingDownload(path, err)
	}
	return nil
}
