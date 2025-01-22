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
		filePath := filepath.Join(targetDirectory, file.Name)

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

		_, err = io.Copy(outFile, rc)
		if err != nil {
			return err
		}
	}

	return nil
}
