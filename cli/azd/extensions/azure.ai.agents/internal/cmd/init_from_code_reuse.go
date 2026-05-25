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
// reuse path. The set intentionally mirrors detectLocalManifest in
// init_from_templates_helpers.go so the same on-disk files trigger either the
// manifest pipeline (when they parse as a valid manifest) or the definition
// reuse pipeline (when they do not).
var agentYamlCandidates = []string{
	"agent.manifest.yaml",
	"agent.manifest.yml",
	"agent.yaml",
	"agent.yml",
}

// findExistingAgentYaml returns the first agent yaml file found in srcDir, or
// an empty string when none exists. The scan is shallow: only srcDir itself
// is checked, never subdirectories. ErrNotExist is treated as "not found" and
// the next candidate is tried; other I/O errors propagate.
//
// This helper is called by RunE in init.go after detectLocalManifest has been
// given the first chance to claim the file. A path returned here is therefore
// guaranteed to not be a valid manifest (otherwise detectLocalManifest would
// have set flags.manifestPointer first); it is either a bare definition or a
// malformed manifest. runReuseDefinition distinguishes the two and produces a
// targeted error for the malformed case.
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
// azure.yaml without rewriting the file or running the from-code scaffolding
// prompts. It is invoked from init.go RunE alongside the manifest detection
// branch, so the user is never asked to pick an init mode when a local
// agent.yaml is already present.
//
// The function deliberately does NOT resolve a Foundry project, prompt for
// model deployments, or enrich the definition with deployment metadata — the
// issue (#7268) constrains the definition path to "less to ask and just setup
// azure.yaml". Users who need a Foundry project bound before azd deploy
// should either delete the definition and rerun init interactively, or set
// AZURE_AI_PROJECT_ID in their azd environment by hand.
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

	// Reuse the same project/environment bootstrap runInitFromManifest uses so
	// addToProject has the project config and env it needs to write azure.yaml.
	projectConfig, err := ensureProject(ctx, flags, azdClient)
	if err != nil {
		return err
	}

	// Mirror InitFromCodeAction.Run: when --src is absolute, convert it to a
	// path relative to the azd project root so azure.yaml's RelativePath is
	// portable across machines.
	if flags.src != "" && filepath.IsAbs(flags.src) {
		relPath, err := filepath.Rel(projectConfig.Path, flags.src)
		if err != nil {
			return fmt.Errorf("failed to convert src path to relative path: %w", err)
		}
		flags.src = relPath
		// srcDir was passed in by the caller; keep it in sync so the messages
		// and addToProject targetDir match.
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

	// Run the same advisory post-init validations the scaffold path emits.
	validatePostInit(srcDir, def.CodeConfiguration)

	return nil
}

// loadAgentDefinitionFile parses path as a bare AgentDefinition (no surrounding
// "template:" wrapper) and validates it. It is the definition-side counterpart
// to agent_yaml.LoadAndValidateAgentManifest.
//
// The parser uses ContainerAgent so a CodeConfiguration block — when present —
// is preserved for the caller to inspect (it drives isCodeDeploy and the
// post-init language detection).
func loadAgentDefinitionFile(path string) (*agent_yaml.ContainerAgent, error) {
	//nolint:gosec // path comes from findExistingAgentYaml against a user-controlled directory
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Reject files that are actually manifests. A valid manifest would have been
	// routed upstream; an invalid one reaching here is most likely a malformed
	// template authored by the user, so the error message points at the cause.
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

	// Run the same schema validation the manifest pipeline runs on the unwrapped
	// template body. This catches missing/invalid kind, missing name, and the
	// kind-specific structural checks before the bare definition is wired into
	// azure.yaml.
	if err := agent_yaml.ValidateAgentDefinition(data); err != nil {
		return nil, err
	}

	var def agent_yaml.ContainerAgent
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	return &def, nil
}
