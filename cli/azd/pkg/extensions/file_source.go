// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// newFileSource creates a new file base registry source.
func newFileSource(name string, path string) (Source, error) {
	registryBytes, err := readSourceFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed reading file '%s', %w", path, err)
	}

	return newJsonSource(name, string(registryBytes))
}

func readSourceFile(filePath string) ([]byte, error) {
	if filepath.IsAbs(filePath) {
		return os.ReadFile(filePath)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	azdConfigPath, err := config.GetUserConfigDir()
	if err != nil {
		return nil, err
	}
	return osutil.ReadContainedFile([]string{cwd, azdConfigPath}, filePath)
}
