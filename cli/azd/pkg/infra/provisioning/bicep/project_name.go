// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/braydonk/yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
)

const maxProjectTagValueLength = 256

func resolveProjectName(projectPath string) (string, error) {
	absoluteProjectPath, err := filepath.Abs(projectPath)
	if err != nil {
		return "", fmt.Errorf("resolving project path: %w", err)
	}

	for _, fileName := range azdcontext.ProjectFileNames {
		projectFilePath := filepath.Join(absoluteProjectPath, fileName)
		projectFile, err := os.ReadFile(projectFilePath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("reading project file %q: %w", projectFilePath, err)
		}

		var config struct {
			Name string `yaml:"name"`
		}
		if err := yaml.Unmarshal(projectFile, &config); err != nil {
			return "", fmt.Errorf("parsing project file %q: %w", projectFilePath, err)
		}

		if projectName := strings.TrimSpace(config.Name); projectName != "" {
			return normalizeProjectName(projectName), nil
		}

		break
	}

	return normalizeProjectName(filepath.Base(absoluteProjectPath)), nil
}

func normalizeProjectName(projectName string) string {
	projectNameRunes := []rune(projectName)
	if len(projectNameRunes) <= maxProjectTagValueLength {
		return projectName
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(projectName)))
	prefixLength := maxProjectTagValueLength - len(hash) - 1
	return string(projectNameRunes[:prefixLength]) + "-" + hash
}
