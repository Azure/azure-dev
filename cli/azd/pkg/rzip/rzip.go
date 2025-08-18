// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rzip

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// OnZipFn is a function that is invoked on each file or directory,
// returning a bool to indicate whether the entry should be included in the final zip.
type OnZipFn func(src string, info os.FileInfo) (bool, error)

// CreateFromDirectory creates a zip archive from the given directory recursively,
// that is suitable for transporting across machines.
//
// It resolves any symlinks it encounters.
//
// An optional function callback onZip can be passed to observe files being included,
// or simply to exclude files.
func CreateFromDirectory(source string, buf *os.File, onZip OnZipFn) error {
	w := zip.NewWriter(buf)

	err := addDirRoot(w, source, onZip)
	if err != nil {
		return err
	}

	return w.Close()
}

func addDirRoot(
	w *zip.Writer,
	src string,
	onZip OnZipFn) error {
	return addDir(w, "", src, onZip, 0)
}

func addDir(
	w *zip.Writer,
	destRoot,
	src string,
	onZip OnZipFn,
	symlinkDepth int) error {
	if symlinkDepth > 40 {
		// too deep, bail out similarly to the 'zip' tool
		log.Println("skipping", src, "too many levels of symbolic links")
		return nil
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		info, err := os.Lstat(s)
		if err != nil {
			return err
		}

		if onZip != nil {
			include, err := onZip(s, info)
			if err != nil {
				return err
			}
			if !include {
				continue
			}
		}

		switch {
		case info.Mode()&os.ModeSymlink != 0:
			err = onSymlink(w, destRoot, s, info, onZip, symlinkDepth)
		case info.IsDir():
			root := filepath.Join(destRoot, info.Name())
			err = addDir(w, root, s, onZip, symlinkDepth)
		default:
			err = addFile(w, destRoot, s, info.Name(), info)
		}

		if err != nil {
			return err
		}
	}
	return err
}

func addFile(
	w *zip.Writer,
	destRoot string,
	src string,
	name string,
	info os.FileInfo) error {
	dest := filepath.Join(destRoot, name)
	header := &zip.FileHeader{
		Name:     strings.ReplaceAll(dest, "\\", "/"),
		Modified: info.ModTime(),
		Method:   zip.Deflate,
	}

	f, err := w.CreateHeader(header)
	if err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	_, err = io.Copy(f, in)
	if err != nil {
		return err
	}

	return nil
}

func onSymlink(
	w *zip.Writer,
	destRoot,
	src string,
	link os.FileInfo,
	onZip OnZipFn,
	symlinkDepth int) error {
	target, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}

	info, err := os.Lstat(target)
	if err != nil {
		return err
	}

	switch {
	case info.IsDir():
		symlinkDepth++
		root := filepath.Join(destRoot, link.Name())
		return addDir(w, root, target, onZip, symlinkDepth)
	default:
		return addFile(w, destRoot, target, link.Name(), info)
	}
}

func ExtractToDirectory(artifactPath string, targetDirectory string) error {
	// Open the ZIP file
	zipReader, err := zip.OpenReader(artifactPath)
	if err != nil {
		return err
	}
	defer zipReader.Close()

	// Ensure the target directory exists
	err = os.MkdirAll(targetDirectory, os.ModePerm)
	if err != nil {
		return err
	}

	// Iterate through each file in the archive
	for _, file := range zipReader.File {
		// Handles file path cleaning directly below
		// nolint:gosec // G305
		filePath := filepath.Join(targetDirectory, file.Name)

		// Prevent path traversal attacks by ensuring file paths remain within targetDirectory
		if !strings.HasPrefix(filePath, filepath.Clean(targetDirectory)+string(os.PathSeparator)) {
			return &os.PathError{
				Op:   "extract",
				Path: filePath,
				Err:  os.ErrPermission,
			}
		}

		if file.FileInfo().IsDir() {
			// Create the directory
			err = os.MkdirAll(filePath, file.Mode())
			if err != nil {
				return err
			}
			continue
		}

		// Create the file
		err = os.MkdirAll(filepath.Dir(filePath), os.ModePerm)
		if err != nil {
			return err
		}

		outFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}
		defer outFile.Close()

		// Extract the file content
		rc, err := file.Open()
		if err != nil {
			return err
		}
		defer rc.Close()

		// nolint:gosec // G110
		_, err = io.Copy(outFile, rc)
		if err != nil {
			return err
		}
	}

	return nil
}

// ExtractTarGzToDirectory extracts a .tar.gz archive to the specified target directory
func ExtractTarGzToDirectory(artifactPath string, targetDirectory string) error {
	// Open the tar.gz file
	file, err := os.Open(artifactPath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	// Ensure the target directory exists
	err = os.MkdirAll(targetDirectory, os.ModePerm)
	if err != nil {
		return err
	}

	// Iterate through each file in the archive
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break // End of archive
		}
		if err != nil {
			return err
		}

		// Handles file path cleaning directly below
		// nolint:gosec // G305
		filePath := filepath.Join(targetDirectory, header.Name)

		// Prevent path traversal attacks by ensuring file paths remain within targetDirectory
		if !strings.HasPrefix(filePath, filepath.Clean(targetDirectory)+string(os.PathSeparator)) {
			return &os.PathError{
				Op:   "extract",
				Path: filePath,
				Err:  os.ErrPermission,
			}
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// Create the directory
			// nolint:gosec // G115
			err = os.MkdirAll(filePath, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

		case tar.TypeReg:
			// Create the file
			err = os.MkdirAll(filepath.Dir(filePath), os.ModePerm)
			if err != nil {
				return err
			}

			// nolint:gosec // G115
			outFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			// Extract the file content
			// nolint:gosec // G110
			_, err = io.Copy(outFile, tarReader)
			outFile.Close()
			if err != nil {
				return err
			}
		}
	}

	return nil
}
