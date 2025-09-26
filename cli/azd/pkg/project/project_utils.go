// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
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
		ig, err := gitignore.NewFromFile(filepath.Join(root, ignoreFile))
		if !errors.Is(err, fs.ErrNotExist) && err != nil {
			return "", fmt.Errorf("reading ignore file: %w", err)
		}

		ignorer = ig
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
		if ignorer != nil && ignorer.Absolute(src, isDir) != nil {
			return false, nil
		} else if ignorer == nil { // default exclusions without ignorefile control
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
					return false, nil
				}
			}
		}

		return true, nil
	}

	if err := rzip.CreateFromDirectory(root, zipFile, onZip); err != nil {
		// if we fail here just do our best to close things out and cleanup
		zipFile.Close()
		os.Remove(zipFile.Name())
		return "", fmt.Errorf("creating deployable zip: %w", err)
	}

	if err := zipFile.Close(); err != nil {
		// may fail but, again, we'll do our best to cleanup here.
		os.Remove(zipFile.Name())
		return "", err
	}

	return zipFile.Name(), nil
}
