// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
		// Asked again, because the check above happened before a transfer that
		// can run for minutes. It is the cheap refusal; writeFileAtomically
		// holds the promise at the moment of replacing.
		if err := refuseExisting(dest, a.force); err != nil {
			return 0, "", err
		}
		if err := writeFileAtomically(dest, body, a.force); err != nil {
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
		if err := claimStagedPath(local, file); err != nil {
			return 0, "", err
		}
		body, err := ec.datasetClient.Open(ctx, content, file)
		if err != nil {
			return 0, "", messages.DownloadingDataset(a.name, version, err)
		}
		// Into the staging directory this call created, so there is no file of
		// the caller's to protect; replaceDir guards the install at the end.
		err = writeFileAtomically(local, body, true)
		_ = body.Close()
		if err != nil {
			return 0, "", err
		}
	}

	if err := replaceDir(staging, dest, a.force); err != nil {
		return 0, "", err
	}
	return len(content.Files), dest, nil
}

// claimStagedPath refuses a second entry that lands where the first one did.
//
// Staging is created empty, so anything already at this path is another entry
// of the same download. Blob names are case sensitive and Windows and macOS are
// not, so `A.jsonl` and `a.jsonl` are one file here -- and writing on would
// leave one where the listing said two while the count still reported both.
// Checked before the transfer rather than after it.
func claimStagedPath(local, entry string) error {
	if _, err := os.Lstat(local); err == nil {
		return messages.DownloadEntriesCollideLocally(entry, filepath.Base(local))
	}
	return nil
}

// replaceDir moves staging onto dest, which may already exist.
//
// Renaming onto an existing directory fails whatever --force said, so the old
// one moves aside first and is discarded only once the new one is in place, and
// is put back if the rename fails: --force is permission to replace the
// destination, not to lose both.
//
// The holding name is created rather than composed. A fixed sibling such as
// `<dest>.azd-replaced` is a path this command does not own, and clearing it to
// make room would destroy whatever a caller had already put there.
//
// force is asked here and not only before the download, because the files are
// fetched in between and a destination can appear while they are. Replacing it
// then destroys a directory the caller never agreed to lose. This narrows the
// window rather than closing it -- there is no portable rename that refuses an
// existing directory -- but it turns silent destruction into a refusal.
func replaceDir(staging, dest string, force bool) error {
	replaced := ""
	if _, err := os.Lstat(dest); err == nil {
		if !force {
			return messages.DownloadDestinationExists(dest)
		}
		held, err := os.MkdirTemp(filepath.Dir(dest), ".azd-replaced-*")
		if err != nil {
			return messages.CannotWriteInDirectory(filepath.Dir(dest), err)
		}
		// Freed so the rename can take the name; it was created only to reserve it.
		if err := os.Remove(held); err != nil {
			return messages.WritingDownload(dest, err)
		}
		if err := os.Rename(dest, held); err != nil {
			return messages.WritingDownload(dest, err)
		}
		replaced = held
	}
	if err := os.Rename(staging, dest); err != nil {
		if replaced != "" {
			// The restore is the whole reason the old directory was moved rather
			// than removed, so a restore that also fails is the case this exists
			// for -- and discarding its error reported only the install failure
			// while the caller's data sat under a temporary name they were never
			// told about. Both are named, and the holding directory with them.
			if restoreErr := os.Rename(replaced, dest); restoreErr != nil {
				return messages.DownloadLeftDestinationAside(dest, replaced, err)
			}
		}
		return messages.WritingDownload(dest, err)
	}
	if replaced != "" {
		_ = os.RemoveAll(replaced)
	}
	return nil
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
//
// replace is --force. Without it the name is claimed rather than merely
// checked: every existence test the caller can run happens before a copy that
// may take minutes, and the rename below replaces whatever it finds by then, so
// a file created during the transfer was destroyed by a command that promised
// not to. O_EXCL either takes the name or reports that something holds it, with
// no gap in between.
func writeFileAtomically(path string, body io.Reader, replace bool) error {
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

	if !replace {
		// #nosec G304 -- path is the destination the caller named, opened
		// exclusively to claim the name rather than to read anything.
		claim, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				return messages.DownloadDestinationExists(path)
			}
			return messages.WritingDownload(path, err)
		}
		if err := claim.Close(); err != nil {
			_ = os.Remove(path)
			return messages.WritingDownload(path, err)
		}
	}

	if err := os.Rename(tmpName, path); err != nil {
		if !replace {
			// The empty claim is this call's own, and the only thing it may
			// take back: anything else under that name predates it.
			_ = os.Remove(path)
		}
		return messages.WritingDownload(path, err)
	}
	return nil
}
