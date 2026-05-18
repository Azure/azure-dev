// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/fatih/color"
)

// validatePostInit runs all post-init validations and prints errors (non-blocking).
// Validations are advisory — they highlight issues that will cause deploy failures
// but do not prevent init from completing.
func validatePostInit(srcDir string, codeConfig *agent_yaml.CodeConfiguration) {
	if codeConfig == nil {
		return
	}

	validateDotnetRuntimeVsCsproj(srcDir, codeConfig.Runtime)
}

// validateDotnetRuntimeVsCsproj checks whether the selected .NET runtime version is compatible
// with the TargetFramework declared in the .csproj file. Prints an error (non-blocking) if:
// - The .csproj cannot be read (user should verify their project structure)
// - The .csproj targets a higher framework version than the selected runtime
func validateDotnetRuntimeVsCsproj(srcDir string, runtime string) {
	if !strings.HasPrefix(runtime, "dotnet_") {
		return
	}

	// Parse selected runtime version (e.g. "dotnet_9" -> 9, "dotnet_10" -> 10)
	runtimeVersionStr := strings.TrimPrefix(runtime, "dotnet_")
	runtimeVersion, err := strconv.Atoi(runtimeVersionStr)
	if err != nil {
		return
	}

	// Find .csproj file in srcDir
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		fmt.Printf("\n%s Could not read project directory %q to validate .NET TargetFramework. "+
			"Please verify your .csproj TargetFramework matches the selected .NET %d runtime before deploying.\n",
			color.RedString("ERROR:"),
			srcDir, runtimeVersion,
		)
		return
	}

	var csprojFound bool
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".csproj") {
			continue
		}
		csprojFound = true

		csprojPath := filepath.Join(srcDir, e.Name())
		data, err := os.ReadFile(csprojPath) //nolint:gosec // path from user project
		if err != nil {
			fmt.Printf("\n%s Could not read %s to validate TargetFramework. "+
				"Please verify it targets net%d.0 or lower before deploying.\n",
				color.RedString("ERROR:"),
				e.Name(), runtimeVersion,
			)
			return
		}

		targetVersion := extractTargetFrameworkVersion(string(data))
		if targetVersion <= 0 {
			fmt.Printf("\n%s Could not parse TargetFramework from %s. "+
				"Please verify it targets net%d.0 or lower before deploying.\n",
				color.RedString("ERROR:"),
				e.Name(), runtimeVersion,
			)
			return
		}

		if targetVersion > runtimeVersion {
			fmt.Printf("\n%s %s targets net%d.0 but selected runtime is .NET %d. "+
				"This will fail during build/deploy.\n"+
				"  Fix: Change <TargetFramework> in %s to net%d.0, or re-run init and select .NET %d runtime.\n",
				color.RedString("ERROR:"),
				e.Name(), targetVersion, runtimeVersion,
				e.Name(), runtimeVersion,
				targetVersion,
			)
		} else {
			fmt.Printf("\n%s .NET runtime validation passed: %s targets net%d.0, selected runtime is .NET %d.\n",
				color.GreenString("OK:"),
				e.Name(), targetVersion, runtimeVersion,
			)
		}
		return // only check the first .csproj found
	}

	if !csprojFound {
		// No .csproj in directory — not necessarily an error for dotnet code deploy
		// (could be a pre-compiled DLL scenario), so skip silently.
		return
	}
}

// extractTargetFrameworkVersion parses the major version number from a .csproj TargetFramework element.
// e.g. "<TargetFramework>net10.0</TargetFramework>" -> 10
// Returns 0 if not found or not parsable.
func extractTargetFrameworkVersion(csprojContent string) int {
	re := regexp.MustCompile(`<TargetFramework>net(\d+)\.\d+</TargetFramework>`)
	matches := re.FindStringSubmatch(csprojContent)
	if len(matches) < 2 {
		return 0
	}
	version, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return version
}
