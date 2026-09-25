// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"azureaiagent/internal/exterrors"
)

// agentYamlCandidates lists the legacy filenames recognized for migration
// guidance. Detection is metadata-only; init never parses these files.
var agentYamlCandidates = []string{
	"agent.manifest.yaml",
	"agent.yaml",
	"agent.manifest.yml",
	"agent.yml",
}

// findExistingAgentYaml returns the first legacy agent YAML filename found
// directly under srcDir, or an empty string when none exists.
func findExistingAgentYaml(srcDir string) (string, error) {
	for _, name := range agentYamlCandidates {
		candidate := filepath.Join(srcDir, name)
		info, err := os.Stat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("checking for %s: %w", candidate, err)
		}
		if info.IsDir() {
			continue
		}
		return candidate, nil
	}

	return "", nil
}

func legacyInitSourceError(path string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidAgentManifest,
		fmt.Sprintf(
			"legacy agent configuration %q is no longer accepted by 'azd ai agent init'",
			filepath.ToSlash(path),
		),
		"Move the agent definition into an azure.ai.agent service in azure.yaml, "+
			"or reference a direct definition from that service with $ref.",
	)
}
