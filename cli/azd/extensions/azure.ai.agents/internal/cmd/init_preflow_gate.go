// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// init_preflow_gate.go decides whether the agent-driven onboarding
// pre-flow (see init_preflow.go) should run on a given `azd ai agent
// init` invocation. The pre-flow is a high-touch interactive flow that
// only makes sense for a true greenfield start; it is intentionally
// suppressed whenever the caller has already signaled explicit intent
// (flags, existing agent setup, prior azd configuration). Keeping the
// gate logic in one place avoids scattering ad-hoc conditionals across
// the init RunE function.

package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// shouldRunPreflow reports whether the agent-driven onboarding pre-flow
// should run for this invocation. Returns (false, nil) for any explicit-
// intent signal, (false, err) when a filesystem probe fails, and
// (true, nil) only for a clean interactive greenfield start.
//
// Skip conditions:
//   - Non-interactive session (--no-prompt / AZD_NO_PROMPT).
//   - Any explicit init-mode or downstream config flag (see
//     hasExplicitInitFlags). These signal "I am scripting this; do not
//     ask me questions."
//   - An existing agent manifest or bare agent.yaml in the source dir.
//     The downstream "use it?" reuse flow at init.go ~728-807 handles
//     that case; asking about coding-agent setup first would be a
//     confusing detour for a re-init.
//   - The current directory already has azd configuration
//     (azure.yaml / .azure/), which almost always means re-init.
func shouldRunPreflow(flags *initFlags, cwd string) (bool, error) {
	if flags.noPrompt {
		return false, nil
	}

	if hasExplicitInitFlags(flags) {
		return false, nil
	}

	checkDir := flags.src
	if checkDir == "" {
		checkDir = "."
	}

	hasAgent, err := hasExistingAgentSetup(checkDir)
	if err != nil {
		return false, err
	}
	if hasAgent {
		return false, nil
	}

	hasAzd, err := hasExistingAzdSetup(cwd)
	if err != nil {
		return false, err
	}
	if hasAzd {
		return false, nil
	}

	return true, nil
}

// hasExplicitInitFlags reports whether the user has set any flag that
// makes the agent-driven onboarding pre-flow redundant or surprising.
//
// --force and --env are intentionally excluded: --force is just an
// overwrite-consent toggle and --env only selects which environment to
// bind, neither implies the user has already decided how to author the
// agent.
func hasExplicitInitFlags(flags *initFlags) bool {
	return flags.manifestPointer != "" ||
		flags.fromCode ||
		flags.src != "" ||
		flags.projectResourceId != "" ||
		flags.modelDeployment != "" ||
		flags.model != "" ||
		flags.agentName != "" ||
		len(flags.protocols) > 0 ||
		flags.deployMode != "" ||
		flags.runtime != "" ||
		flags.entryPoint != "" ||
		flags.depResolution != ""
}

// hasExistingAgentSetup reports whether dir already contains an agent
// manifest or bare agent.yaml that the downstream init flow would offer
// to reuse. The probes intentionally mirror detectLocalManifest and
// findExistingAgentYaml so the gate and the downstream flow stay in
// sync; the duplicate stat calls are a cheap price for skipping the
// pre-flow before any prompts render.
func hasExistingAgentSetup(dir string) (bool, error) {
	manifest, err := detectLocalManifest(dir)
	if err != nil {
		return false, fmt.Errorf("preflow gate: checking for existing manifest: %w", err)
	}
	if manifest != "" {
		return true, nil
	}

	existing, err := findExistingAgentYaml(dir)
	if err != nil {
		return false, fmt.Errorf("preflow gate: checking for existing agent definition: %w", err)
	}
	return existing != "", nil
}

// azdProjectMarkers lists the file/directory names that indicate a
// directory already has azd configuration. Both YAML extensions are
// accepted because core azd recognizes either.
var azdProjectMarkers = []string{"azure.yaml", "azure.yml", ".azure"}

// hasExistingAzdSetup reports whether cwd already contains azd
// configuration. The check is shallow (no parent-directory walk) to
// match the downstream init flow, which also operates on cwd.
func hasExistingAzdSetup(cwd string) (bool, error) {
	if cwd == "" {
		cwd = "."
	}
	for _, name := range azdProjectMarkers {
		_, err := os.Stat(filepath.Join(cwd, name))
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("preflow gate: stat %s: %w", name, err)
		}
	}
	return false, nil
}
