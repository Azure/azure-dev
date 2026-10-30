// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateFunctionProject checks required source files for a Flex Consumption Function App.
func ValidateFunctionProject(projectPath string, language ServiceLanguageKind) error {
	if info, err := os.Stat(filepath.Join(projectPath, "host.json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no host.json found in Function App project %q", projectPath)
		}
		return fmt.Errorf("checking Function App project: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("host.json must be a file in Function App project %q", projectPath)
	}
	if language.IsDotNet() {
		return ValidateDotNetFunctionProject(projectPath)
	}
	return nil
}

// ValidateDotNetFunctionProject rejects in-process .NET projects, which Flex Consumption cannot host.
func ValidateDotNetFunctionProject(projectPath string) error {
	files, err := os.ReadDir(projectPath)
	if err != nil {
		return fmt.Errorf("finding .NET Function App project: %w", err)
	}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		switch filepath.Ext(file.Name()) {
		case ".csproj", ".fsproj", ".vbproj":
		default:
			continue
		}
		path := filepath.Join(projectPath, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading .NET Function App project %q: %w", path, err)
		}
		if strings.Contains(strings.ToLower(string(data)), "microsoft.net.sdk.functions") {
			return fmt.Errorf("Flex Consumption requires a .NET isolated Function App; %q uses in-process Functions", path)
		}
	}
	return nil
}
