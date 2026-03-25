// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/rzip"
	"github.com/denormal/go-gitignore"
)

// CreateDeployableZip creates a zip file of a folder, recursively.
// Returns the path to the created zip file or an error if it fails.
func createDeployableZip(svc *ServiceConfig, root string) (string, error) {
	filePath := filepath.Join(
		os.TempDir(),
		fmt.Sprintf("%s-%s-azddeploy-%d.zip", svc.Project.Name, svc.Name, time.Now().Unix()))
	zipFile, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("failed when creating zip package to deploy %s: %w", svc.Name, err)
	}

	ignoreFile := svc.Host.IgnoreFile()
	var ignorer gitignore.GitIgnore
	if ignoreFile != "" {
		ignoreFilePath := filepath.Join(root, ignoreFile)
		contents, err := os.ReadFile(ignoreFilePath)
		if errors.Is(err, fs.ErrNotExist) {
			// no ignore file, use defaults below
		} else if err != nil {
			zipFile.Close()
			os.Remove(zipFile.Name()) //nolint:gosec // G703: zipFile.Name() is our own temp file, not user-controlled
			return "", fmt.Errorf("reading ignore file: %w", err)
		} else {
			// Strip UTF-8 BOM if present, then parse from in-memory contents.
			contents = stripUTF8BOM(contents)
			ignorer = gitignore.New(bytes.NewReader(contents), root, nil)
		}
	}

	// apply exclusions for zip deployment
	onZip := func(src string, info os.FileInfo) (bool, error) {
		name := info.Name()
		isDir := info.IsDir()

		// resolve symlink if needed
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Stat(src)
			if err != nil {
				return false, err
			}
			isDir = target.IsDir()
		}

		if name == ".azure" && isDir {
			return false, nil
		}

		if name == ignoreFile && !isDir {
			return false, nil
		}

		// host specific exclusions
		if svc.Host == AzureFunctionTarget {
			if name == "local.settings.json" && !isDir {
				return false, nil
			}
		}

		// apply exclusions from ignore file
		if ignorer != nil {
			if match := ignorer.Absolute(src, isDir); match != nil && match.Ignore() {
				return false, nil
			}
		} else { // default exclusions without ignorefile control
			if svc.Language == ServiceLanguagePython {
				if isDir {
					// check for .venv containing pyvenv.cfg
					if _, err := os.Stat(filepath.Join(src, "pyvenv.cfg")); err == nil {
						return false, nil
					}

					if strings.ToLower(name) == "__pycache__" {
						return false, nil
					}
				}
			} else if svc.Language == ServiceLanguageJavaScript || svc.Language == ServiceLanguageTypeScript {
				if name == "node_modules" && isDir {
					if svc.RemoteBuild != nil && !*svc.RemoteBuild {
						// if remote build is false, we do not exclude node_modules by default
						return true, nil
					}

					return false, nil
				}
			}
		}

		return true, nil
	}

	if err := rzip.CreateFromDirectory(root, zipFile, onZip); err != nil {
		// if we fail here just do our best to close things out and cleanup
		zipFile.Close()
		os.Remove(zipFile.Name()) //nolint:gosec // G703: temp file cleanup
		return "", fmt.Errorf("creating deployable zip: %w", err)
	}

	if err := zipFile.Close(); err != nil {
		// may fail but, again, we'll do our best to cleanup here.
		os.Remove(zipFile.Name()) //nolint:gosec // G703: temp file cleanup
		return "", err
	}

	return zipFile.Name(), nil
}

// utf8BOM is the byte order mark that some Windows editors prepend to UTF-8 files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// stripUTF8BOM removes a leading UTF-8 BOM from the given byte slice if present.
// The BOM breaks gitignore pattern parsing because the invisible bytes become part of the first pattern.
func stripUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, utf8BOM)
}
