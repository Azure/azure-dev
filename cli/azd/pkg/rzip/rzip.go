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
