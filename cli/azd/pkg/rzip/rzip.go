// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rzip

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
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

const (
	permissionDirectory = 0755
)

func Extract(ctx context.Context, zipFile string, target string) error {
	zipReader, err := zip.OpenReader(zipFile)
	if err != nil {
		return fmt.Errorf("opening zip file %s: %w", zipFile, err)
	}
	defer zipReader.Close()

	// loop and create each file (skip directories)
	for _, zipItemFile := range zipReader.File {
		if !zipItemFile.Mode().IsDir() {
			// Create the directory if this is a zip inside some other folders
			fullPathForFile := path.Join(target, zipItemFile.Name)
			if err = os.MkdirAll(path.Dir(fullPathForFile), permissionDirectory); err != nil {
				return fmt.Errorf("extracting zip file: %w", err)
			}

			// read zip item
			zipItemReader, err := zipItemFile.Open()
			if err != nil {
				return fmt.Errorf("reading zip file: %w", err)
			}

			// Create file
			extractedFile, err := os.Create(fullPathForFile)
			if err != nil {
				return fmt.Errorf("extracting zip file: %w", err)
			}
			defer extractedFile.Close()
			if _, err := extractedFile.ReadFrom(zipItemReader); err != nil {
				return fmt.Errorf("extracting zip file: %w", err)
			}
		}
	}

	return nil
}
