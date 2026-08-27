// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templates

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
)

// newFileTemplateSource creates a new template source from a file.
func newFileTemplateSource(name string, path string) (Source, error) {
	templateBytes, err := readTemplateFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed reading file '%s', %w", path, err)
	}

	return newJsonTemplateSource(name, string(templateBytes))
}

func readTemplateFile(filePath string) ([]byte, error) {
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
