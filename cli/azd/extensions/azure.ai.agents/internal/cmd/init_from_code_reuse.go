// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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

func sameInitSourceDirectory(left, right string) bool {
	left, leftErr := filepath.Abs(left)
	right, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if left == right {
		return true
	}

	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func validateExplicitInitSource(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	sourceDir string,
) error {
	if strings.TrimSpace(sourceDir) == "" {
		return nil
	}

<<<<<<< HEAD
	def, err := loadAgentDefinitionFile(existingPath)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent definition in %s is invalid: %s", displayPath, err),
			fmt.Sprintf("Fix %s and retry, or remove the file to start a fresh init.", displayPath),
		)
	}
	recordInitDefinition(ctx, def)

	fmt.Println(color.HiBlackString(
		"Detected existing agent definition: %s (name: %s).",
		displayPath, def.Name,
	))

	projectConfig, err := ensureProject(ctx, flags, azdClient, ".")
=======
	legacyPath, err := findExistingAgentYaml(sourceDir)
>>>>>>> refs/rewritten/onto
	if err != nil {
		return err
	}

	response, projectErr := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if projectErr != nil || response.GetProject() == nil {
		if legacyPath != "" {
			return legacyInitSourceError(legacyPath)
		}
		return nil
	}

	configuredServices, err := projectAgentServicesFrom(
		response.GetProject().GetServices(),
		response.GetProject().GetPath(),
	)
	if err != nil {
		return err
	}
	if legacyPath == "" {
		return nil
	}

	for _, service := range configuredServices {
		serviceDir := filepath.Join(response.GetProject().GetPath(), filepath.FromSlash(service.RelativePath))
		if sameInitSourceDirectory(sourceDir, serviceDir) {
			return nil
		}
	}

	return legacyInitSourceError(legacyPath)
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
