// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/otiai10/copy"
)

// CreateDeployableZip creates a zip file of a folder, recursively.
// Returns the path to the created zip file or an error if it fails.
func createDeployableZip(appName string, path string) (string, error) {
	// TODO: should probably avoid picking up files that weren't meant to be deployed (ie, local .env files, etc..)
	zipFile, err := os.CreateTemp("", "azddeploy*.zip")
	if err != nil {
		return "", fmt.Errorf("failed when creating zip package to deploy %s: %w", appName, err)
	}

	if err := rzip.CreateFromDirectory(path, zipFile); err != nil {
		// if we fail here just do our best to close things out and cleanup
		zipFile.Close()
		os.Remove(zipFile.Name())
		return "", err
	}

	if err := zipFile.Close(); err != nil {
		// may fail but, again, we'll do our best to cleanup here.
		os.Remove(zipFile.Name())
		return "", err
	}

	return zipFile.Name(), nil
}

// createDeployableTar creates a tar file of a folder, recursively, and put the tar file in the given directory dir.
// Returns the path to the created tar file or an error if it fails.
func createDeployableTar(appName, path, dir, packageTarName string) (string, error) {
	ext := ".tar.gz"
	tarFile, err := os.Create(filepath.Join(dir, packageTarName+ext))
	if err != nil {
		return "", fmt.Errorf("failed when creating tar package to deploy %s: %w", appName, err)
	}

	if err := compressFolderToTarGz(path, tarFile); err != nil {
		// if we fail here just do our best to close things out and cleanup
		tarFile.Close()
		os.Remove(tarFile.Name())
		return "", err
	}

	if err := tarFile.Close(); err != nil {
		// may fail but, again, we'll do our best to cleanup here.
		os.Remove(tarFile.Name())
		return "", err
	}

	return tarFile.Name(), nil
}

func compressFolderToTarGz(path string, buf io.Writer) error {
	zr := gzip.NewWriter(buf)
	tw := tar.NewWriter(zr)

	// walk through every file in the folder
	filepath.Walk(path, func(file string, fi os.FileInfo, err error) error {
		// generate tar header
		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}

		// update the name to correctly reflect the desired destination when untaring
		header.Name = strings.TrimPrefix(strings.TrimPrefix(file, path), string(filepath.Separator))

		// write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		// if not a dir, write file content
		if !fi.IsDir() {
			data, err := os.Open(file)
			defer func() {
				_ = data.Close()
			}()
			if err != nil {
				return err
			}
			if _, err := io.Copy(tw, data); err != nil {
				return err
			}
		}
		return nil
	})

	// produce tar
	if err := tw.Close(); err != nil {
		return err
	}
	// produce gzip
	if err := zr.Close(); err != nil {
		return err
	}
	return nil
}

// excludeDirEntryCondition resolves when a file or directory should be considered or not as part of build, when build is a
// copy-paste source strategy. Return true to exclude the directory entry.
type excludeDirEntryCondition func(path string, file os.FileInfo) bool

// buildForZipOptions provides a set of options for doing build for zip
type buildForZipOptions struct {
	excludeConditions []excludeDirEntryCondition
}

// buildForZip is use by projects which build strategy is to only copy the source code into a folder which is later
// zipped for packaging. For example Python and Node framework languages. buildForZipOptions provides the specific
// details for each language which should not be ever copied.
func buildForZip(src, dst string, options buildForZipOptions) error {

	// these exclude conditions applies to all projects
	options.excludeConditions = append(options.excludeConditions, globalExcludeAzdFolder)

	return copy.Copy(src, dst, copy.Options{
		Skip: func(srcInfo os.FileInfo, src, dest string) (bool, error) {
			for _, checkExclude := range options.excludeConditions {
				if checkExclude(src, srcInfo) {
					return true, nil
				}
			}
			return false, nil
		},
	})
}

func globalExcludeAzdFolder(path string, file os.FileInfo) bool {
	return file.IsDir() && file.Name() == ".azure"
}
