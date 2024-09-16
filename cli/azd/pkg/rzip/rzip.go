// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rzip

import (
	"archive/zip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func CreateFromDirectory(source string, buf *os.File) error {
	w := zip.NewWriter(buf)
	err := filepath.WalkDir(source, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Use os.Lstat to get file info without following symlinks
		fileInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}

		// Skip symbolic links
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Add directories to the zip archive
		relativePath := strings.Replace(
			strings.TrimPrefix(
				strings.TrimPrefix(path, source),
				string(filepath.Separator)), "\\", "/", -1)

		if fileInfo.IsDir() {
			// Add trailing slash for directories in zip
			relativePath += "/"
		}

		// Create a zip header based on file info
		header, err := zip.FileInfoHeader(fileInfo)
		if err != nil {
			return err
		}
		header.Name = relativePath

		// Add files with compression
		if !fileInfo.IsDir() {
			header.Method = zip.Deflate
		}

		// Create the header in the zip file
		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		// Only copy contents for files (not directories)
		if !fileInfo.IsDir() {
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()

			_, err = io.Copy(writer, in)
			if err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	return w.Close()
}
