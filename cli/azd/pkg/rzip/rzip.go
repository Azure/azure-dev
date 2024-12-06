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

// CreateFromDirectory compresses a directory into a zip file.
func CreateFromDirectory(source string, buf *os.File) error {
	w := zip.NewWriter(buf)
	defer w.Close()

	// Walk through the source directory
	err := filepath.WalkDir(source, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Fetch file info (Lstat avoids following symlinks)
		fileInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}

		// Skip symbolic links
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Create relative path and normalize it for zip
		relativePath := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(path, source), string(filepath.Separator)))

		// Handle directories by adding a trailing slash
		if fileInfo.IsDir() {
			relativePath += "/"
		}

		// Create zip header from the file info
		header, err := zip.FileInfoHeader(fileInfo)
		if err != nil {
			return err
		}

		header.Name = relativePath
		header.Modified = fileInfo.ModTime()

		// Compress files (leave directories uncompressed)
		if !fileInfo.IsDir() {
			header.Method = zip.Deflate
		}

		// Write the header to the zip
		writer, err := w.CreateHeader(header)
		if err != nil {
			return err
		}

		// Write the file's content if it's not a directory
		if !fileInfo.IsDir() {
			if err := writeFileToZip(writer, path); err != nil {
				return err
			}
		}

		return nil
	})

	return err
}

// writeFileToZip writes the contents of a file to the zip writer.
func writeFileToZip(writer io.Writer, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(writer, file)
	return err
}
