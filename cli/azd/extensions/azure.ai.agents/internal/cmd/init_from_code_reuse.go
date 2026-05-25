// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"go.yaml.in/yaml/v3"
)

// agentYamlCandidates lists the file names (in priority order) scanned by the
// reuse path. Order matches detectLocalManifest in init_from_templates_helpers.go.
var agentYamlCandidates = []string{
	"agent.manifest.yaml",
	"agent.yaml",
	"agent.manifest.yml",
	"agent.yml",
}

// findExistingAgentYaml returns the first agent yaml file found in srcDir, or
// an empty string when none exists. The scan is shallow.
//
// Called from RunE after detectLocalManifest. A path returned here is either a
// bare definition or a malformed manifest; runReuseDefinition distinguishes them.
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

// runReuseDefinition wires an existing bare agent.yaml definition into
// azure.yaml without rewriting the file or running the from-code prompts.
//
// Foundry project resolution and model deployment selection are intentionally
// skipped (issue #7268: "less to ask and just setup azure.yaml"). Users who
// need a project bound before azd deploy can set AZURE_AI_PROJECT_ID by hand.
func runReuseDefinition(
	ctx context.Context,
	flags *initFlags,
	azdClient *azdext.AzdClient,
	httpClient *http.Client,
	srcDir string,
	existingPath string,
) error {
	displayPath, err := filepath.Rel(srcDir, existingPath)
	if err != nil || displayPath == "" {
		displayPath = existingPath
	}

	def, err := loadAgentDefinitionFile(existingPath)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("agent definition in %s is invalid: %s", displayPath, err),
			fmt.Sprintf("Fix %s and retry, or remove the file to start a fresh init.", displayPath),
		)
	}

	fmt.Println(color.HiBlackString(
		"Detected existing agent definition: %s (name: %s).",
		displayPath, def.Name,
	))

	projectConfig, err := ensureProject(ctx, flags, azdClient)
	if err != nil {
		return err
	}

	// Mirror InitFromCodeAction.Run: convert absolute --src to project-relative
	// so azure.yaml's RelativePath stays portable.
	if flags.src != "" && filepath.IsAbs(flags.src) {
		relPath, err := filepath.Rel(projectConfig.Path, flags.src)
		if err != nil {
			return fmt.Errorf("failed to convert src path to relative path: %w", err)
		}
		flags.src = relPath
		srcDir = relPath
	}

	env := getExistingEnvironment(ctx, flags.env, azdClient)
	if env == nil {
		envName := flags.env
		if envName == "" {
			envName = sanitizeAgentName(def.Name + "-dev")
		}
		env, err = createNewEnvironment(ctx, azdClient, envName)
		if err != nil {
			return fmt.Errorf("failed to create azd environment: %w", err)
		}
		flags.env = env.Name
	}

	action := &InitFromCodeAction{
		azdClient:     azdClient,
		flags:         flags,
		projectConfig: projectConfig,
		environment:   env,
		httpClient:    httpClient,
	}

	isCodeDeploy := def.CodeConfiguration != nil
	if err := action.addToProject(ctx, srcDir, def.Name, isCodeDeploy); err != nil {
		return fmt.Errorf("failed to add agent to azure.yaml: %w", err)
	}

	fmt.Println(color.HiBlackString("Reusing existing %s (name: %s).", displayPath, def.Name))

	validatePostInit(srcDir, def.CodeConfiguration)

	return nil
}

// loadAgentDefinitionFile parses path as a bare AgentDefinition (no surrounding
// "template:" wrapper) and runs the same schema validation the manifest
// pipeline does.
func loadAgentDefinitionFile(path string) (*agent_yaml.ContainerAgent, error) {
	//nolint:gosec // path comes from findExistingAgentYaml against a user-controlled directory
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Reject manifest-shaped files. A valid manifest would have been routed
	// upstream; an invalid one reaching here is a malformed template.
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if _, hasTemplate := top["template"]; hasTemplate {
		return nil, fmt.Errorf(
			"file contains a 'template:' field but did not parse as a valid agent manifest; " +
				"fix the manifest schema and retry",
		)
	}

	if err := agent_yaml.ValidateAgentDefinition(data); err != nil {
		return nil, err
	}

	var def agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &def, nil
}
