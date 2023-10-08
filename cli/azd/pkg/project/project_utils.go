// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/otiai10/copy"
	// "github.com/walle/targz"
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

// createDeployableTar creates a tar file of a folder, recursively.
// Returns the path to the created tar file or an error if it fails.
func createDeployableTar(appName, path string) (string, error) {
	tarFile, err := os.CreateTemp("", "app*.tar.gz")

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
	// outputPath, err := os.MkdirTemp("", "azd")
	// tarFile := filepath.Join(outputPath, "app.tar.gz")
	// if err = Compress(path, tarFile); err != nil {
	// 	// may fail but, again, we'll do our best to cleanup here.
	// 	os.Remove(tarFile)
	// 	return "", err
	// }

	// return tarFile, nil
}

func compressFolderToTarGz(path string, buf io.Writer) error {
	zr := gzip.NewWriter(buf)
	tw := tar.NewWriter(zr)
	fmt.Println("source path: " + path)

	// walk through every file in the folder
	filepath.WalkDir(path, func(file string, info fs.DirEntry, err error) error {
		fmt.Println("each file path: " + file)
		fi, err := info.Info()
		// generate tar header
		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}

		// update the name to correctly reflect the desired destination when untaring
		// if header.Name = strings.TrimPrefix(strings.TrimPrefix(file, path), string(filepath.Separator)); header.Name == "" {
		// 	header.Name = "."
		// }
		// header.Name = strings.TrimPrefix(strings.TrimPrefix(file, path), string(filepath.Separator))
		header.Name = strings.Replace(strings.TrimPrefix(strings.TrimPrefix(file, path), string(filepath.Separator)), string(filepath.Separator), "/", -1)
		// header.Name = string(filepath.Separator) + strings.TrimPrefix(strings.TrimPrefix(file, path), string(filepath.Separator))
		// header.Name = "/" + strings.Replace(strings.TrimPrefix(strings.TrimPrefix(file, path), string(filepath.Separator)), string(filepath.Separator), "/", -1)
		// if header.Name = strings.Replace(strings.TrimPrefix(strings.TrimPrefix(file, path), string(filepath.Separator)), string(filepath.Separator), "/", -1); header.Name == "" {
		// 	header.Name = "."
		// }

		// header.Name = file
		fmt.Println("file header name: " + header.Name)

		// write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		// if not a dir, write file content
		if !info.IsDir() {

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

func Compress(inputFilePath, outputFilePath string) (err error) {
	inputFilePath = stripTrailingSlashes(inputFilePath)
	inputFilePath, outputFilePath, err = makeAbsolute(inputFilePath, outputFilePath)
	if err != nil {
		return err
	}
	undoDir, err := mkdirAll(filepath.Dir(outputFilePath), 0755)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			undoDir()
		}
	}()

	err = compress(inputFilePath, outputFilePath, inputFilePath)
	if err != nil {
		return err
	}

	return nil
}

// Creates all directories with os.MakedirAll and returns a function to remove the first created directory so cleanup is possible.
func mkdirAll(dirPath string, perm os.FileMode) (func(), error) {
	var undoDir string

	for p := dirPath; ; p = path.Dir(p) {
		finfo, err := os.Stat(p)

		if err == nil {
			if finfo.IsDir() {
				break
			}

			finfo, err = os.Lstat(p)
			if err != nil {
				return nil, err
			}

			if finfo.IsDir() {
				break
			}

			return nil, &os.PathError{"mkdirAll", p, syscall.ENOTDIR}
		}

		if os.IsNotExist(err) {
			undoDir = p
		} else {
			return nil, err
		}
	}

	if undoDir == "" {
		return func() {}, nil
	}

	if err := os.MkdirAll(dirPath, perm); err != nil {
		return nil, err
	}

	return func() { os.RemoveAll(undoDir) }, nil
}

// Remove trailing slash if any.
func stripTrailingSlashes(path string) string {
	if len(path) > 0 && path[len(path)-1] == '/' {
		path = path[0 : len(path)-1]
	}

	return path
}

// Make input and output paths absolute.
func makeAbsolute(inputFilePath, outputFilePath string) (string, string, error) {
	inputFilePath, err := filepath.Abs(inputFilePath)
	if err == nil {
		outputFilePath, err = filepath.Abs(outputFilePath)
	}

	return inputFilePath, outputFilePath, err
}

// The main interaction with tar and gzip. Creates a archive and recursivly adds all files in the directory.
// The finished archive contains just the directory added, not any parents.
// This is possible by giving the whole path exept the final directory in subPath.
func compress(inPath, outFilePath, subPath string) (err error) {
	fmt.Println("sub input dir: " + subPath)
	files, err := ioutil.ReadDir(inPath)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		return errors.New("targz: input directory is empty")
	}

	file, err := os.Create(outFilePath)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(outFilePath)
		}
	}()

	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)

	err = writeDirectory(inPath, tarWriter, subPath)
	if err != nil {
		return err
	}

	err = tarWriter.Close()
	if err != nil {
		return err
	}

	err = gzipWriter.Close()
	if err != nil {
		return err
	}

	err = file.Close()
	if err != nil {
		return err
	}

	return nil
}

// Read a directy and write it to the tar writer. Recursive function that writes all sub folders.
func writeDirectory(directory string, tarWriter *tar.Writer, subPath string) error {
	files, err := ioutil.ReadDir(directory)
	if err != nil {
		return err
	}

	for _, file := range files {
		currentPath := filepath.Join(directory, file.Name())
		if file.IsDir() {
			err := writeDirectory(currentPath, tarWriter, subPath)
			if err != nil {
				return err
			}
		} else {
			err = writeTarGz(currentPath, tarWriter, file, subPath)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// Write path without the prefix in subPath to tar writer.
func writeTarGz(path string, tarWriter *tar.Writer, fileInfo os.FileInfo, subPath string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	evaledPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}

	subPath, err = filepath.EvalSymlinks(subPath)
	if err != nil {
		return err
	}

	link := ""
	if evaledPath != path {
		link = evaledPath
	}

	header, err := tar.FileInfoHeader(fileInfo, link)
	if err != nil {
		return err
	}
	header.Name = evaledPath[len(subPath):]

	err = tarWriter.WriteHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(tarWriter, file)
	if err != nil {
		return err
	}

	return err
}
