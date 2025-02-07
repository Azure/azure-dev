// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rzip

import (
	"archive/zip"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// CreateFromDirectory creates a zip archive from the given directory recursively,
// that is suitable for transporting across machines.
//
// It resolves any symlinks it encounters.
func CreateFromDirectory(source string, buf *os.File) error {
	w := zip.NewWriter(buf)

	err := addDirRoot(w, source)
	if err != nil {
		return err
	}

	return w.Close()
}

func addDirRoot(
	w *zip.Writer,
	src string) error {
	return addDir(w, "", src, 0)
}

func addDir(
	w *zip.Writer,
	destRoot,
	src string,
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

		switch {
		case info.Mode()&os.ModeSymlink != 0:
			err = onSymlink(w, destRoot, s, info, symlinkDepth)
		case info.IsDir():
			root := filepath.Join(destRoot, info.Name())
			err = addDir(w, root, s, symlinkDepth)
		default:
			err = addFile(w, destRoot, s, info)
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
	info os.FileInfo) error {
	dest := filepath.Join(destRoot, info.Name())
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
		return addDir(w, root, target, symlinkDepth)
	default:
		return addFile(w, destRoot, target, link)
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
