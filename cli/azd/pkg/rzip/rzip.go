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

	"github.com/denormal/go-gitignore"
)

func CreateFromDirectory(source string, buf *os.File) error {
	w := zip.NewWriter(buf)
	err := filepath.WalkDir(source, func(path string, info fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}
		fileInfo, err := info.Info()
		if err != nil {
			return err
		}

		header := &zip.FileHeader{
			Name: strings.Replace(
				strings.TrimPrefix(
					strings.TrimPrefix(path, source),
					string(filepath.Separator)), "\\", "/", -1),
			Modified: fileInfo.ModTime(),
			Method:   zip.Deflate,
		}

		f, err := w.CreateHeader(header)
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, in)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	return w.Close()
}

// CreateFromDirectoryWithIgnore creates a zip archive from the contents of a directory, excluding files
// that match any of the provided ignore rules.
func CreateFromDirectoryWithIgnore(srcDir string, writer io.Writer, ignoreMatchers []gitignore.GitIgnore) error {
	zipWriter := zip.NewWriter(writer)
	defer zipWriter.Close()

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get the relative path of the file to ensure the root directory isn't included in the zip file
		relativePath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if relativePath == "." {
			return nil
		}

		// Check if the file should be ignored based on the ignore matchers
		isDir := info.IsDir()
		if shouldIgnore(relativePath, isDir, ignoreMatchers) {
			if isDir {
				// If a directory should be ignored, skip its contents as well
				return filepath.SkipDir
			}
			// Otherwise, just skip the file
			return nil
		}

		// Add the file or directory to the zip archive
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relativePath

		// Ensure directories are properly handled
		if isDir {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		if !isDir {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(writer, file)
			if err != nil {
				return err
			}
		}

		return nil
	})

	return err
}

// shouldIgnore determines whether a file or directory should be ignored based on the provided ignore matchers.
func shouldIgnore(relativePath string, isDir bool, ignoreMatchers []gitignore.GitIgnore) bool {
	for _, matcher := range ignoreMatchers {
		if match := matcher.Relative(relativePath, isDir); match != nil && match.Ignore() {
			return true
		}
	}
	return false
}
