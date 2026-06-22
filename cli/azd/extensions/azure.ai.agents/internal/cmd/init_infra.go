// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"
	"azureaiagent/internal/synthesis"

	"github.com/fatih/color"
	"go.yaml.in/yaml/v3"
)

// ejectArtifact records one file the eject step produced under ./infra/.
// Paths are forward-slash relative to projectRoot so the success output is
// stable across operating systems.
type ejectArtifact struct {
	relPath string // e.g. "infra/main.bicep"
	bytes   int    // size of the file just written
}

// validateStandaloneEjectArgs refuses init-driving inputs that the
// standalone-eject branch would silently drop. `--infra` on an existing
// project runs eject only; honoring a positional path, -m, or --src would
// falsely imply the input was acted upon.
func validateStandaloneEjectArgs(args []string, flags *initFlags) error {
	if len(args) == 0 && flags.manifestPointer == "" && flags.src == "" {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeInfraEjectConflictingArguments,
		"`--infra` on an existing project runs eject only and does not "+
			"accept a positional path, -m/--manifest, or --src",
		"drop the extra argument and run `azd ai agent init --infra` from the project root, "+
			"or remove --infra to run the normal init flow",
	)
}

// ejectInfra synthesizes the embedded Bicep templates from azure.yaml and
// writes them into projectRoot/infra/. Invoked by `azd ai agent init --infra`
// either after a fresh init or as a standalone eject on an existing project.
//
// Refuse conditions:
//
//   - azure.yaml is missing -> CodeInfraEjectAzureYamlMissing
//   - no service has a Foundry host -> CodeInfraEjectNoFoundryService
//   - ./infra/ already exists -> CodeInfraEjectExists
//
// On success it prints the summary block and returns nil. It does NOT modify
// azure.yaml; the declared infra.provider is left unchanged.
func ejectInfra(projectRoot string) error {
	yamlPath := filepath.Join(projectRoot, "azure.yaml")
	//nolint:gosec // G304: azure.yaml under the caller-supplied azd project root
	rawYAML, err := os.ReadFile(yamlPath)
	if err != nil {
		if os.IsNotExist(err) {
			return exterrors.Validation(
				exterrors.CodeInfraEjectAzureYamlMissing,
				"azure.yaml not found in the current directory; "+
					"`azd ai agent init --infra` requires an existing azd agent project",
				"run `azd ai agent init` first to create azure.yaml, then re-run with --infra",
			)
		}
		return fmt.Errorf("read azure.yaml: %w", err)
	}

	svcName, err := findFoundryServiceForEject(rawYAML)
	if err != nil {
		return err
	}

	infraDir := filepath.Join(projectRoot, "infra")
	if _, err := os.Stat(infraDir); err == nil {
		return exterrors.Validation(
			exterrors.CodeInfraEjectExists,
			"`./infra/` already exists",
			"to regenerate from azure.yaml, delete the infra directory and run the command again",
		)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat infra directory: %w", err)
	}

	res, err := synthesis.Synthesize(synthesis.Input{
		RawAzureYAML:  rawYAML,
		ServiceName:   svcName,
		AcceptedHosts: project.FoundryServiceHosts,
	})
	if err != nil {
		// Reuse the provider's vocabulary so eject and provision report
		// consistent codes for the same azure.yaml problems.
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("synthesize foundry service %q: %s", svcName, err),
			"check the deployments/agents fields under your foundry service",
		)
	}

	written, err := writeEmbeddedTemplates(infraDir)
	if err != nil {
		return err
	}

	paramsArtifact, err := writeParametersFile(infraDir, res.Parameters)
	if err != nil {
		return err
	}
	written = append(written, paramsArtifact)
	slices.SortFunc(written, func(a, b ejectArtifact) int {
		return strings.Compare(a.relPath, b.relPath)
	})

	printEjectSummary(written)
	return nil
}

// findFoundryServiceForEject scans azure.yaml for a service whose host is in
// project.FoundryServiceHosts and returns its name, using eject-specific error
// codes so telemetry can distinguish init-time eject from provision failures.
func findFoundryServiceForEject(raw []byte) (string, error) {
	type svc struct {
		Host string `yaml:"host"`
	}
	type root struct {
		Services map[string]svc `yaml:"services"`
	}

	var r root
	if err := yaml.Unmarshal(raw, &r); err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}

	var matches []string
	for name, s := range r.Services {
		if slices.Contains(project.FoundryServiceHosts, s.Host) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return "", exterrors.Dependency(
			exterrors.CodeInfraEjectNoFoundryService,
			fmt.Sprintf("no azure.ai.* services found in azure.yaml (looking for host in %v); "+
				"nothing to eject", project.FoundryServiceHosts),
			fmt.Sprintf("add a service with `host: %s` to azure.yaml, "+
				"or remove --infra to run init normally", project.FoundryServiceHosts[0]),
		)
	case 1:
		return matches[0], nil
	default:
		// Sort for deterministic error message; map iteration order is
		// randomized and would otherwise produce flaky tests.
		slices.Sort(matches)
		return "", exterrors.Dependency(
			exterrors.CodeInfraEjectMultipleFoundryServices,
			fmt.Sprintf("multiple services declare a foundry host %v (%v); only one is supported",
				project.FoundryServiceHosts, matches),
			"keep a single foundry service per project",
		)
	}
}

// writeEmbeddedTemplates copies every file under the synthesizer's embedded
// templates/ root into infraDir, preserving the relative tree, and returns the
// files written (with sizes). On any error it removes the partial infraDir.
//
// main.arm.json (the pre-compiled ARM JSON) is skipped: eject hands the user
// the human-readable Bicep, and the embedded JSON would be stale once they
// edit main.bicep.
func writeEmbeddedTemplates(infraDir string) (_ []ejectArtifact, retErr error) {
	//nolint:gosec // G301: ejected infra/ directory must be readable/traversable by IDEs, Git, and CI
	if err := os.MkdirAll(infraDir, 0o755); err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("create infra directory: %s", err),
		)
	}
	defer func() {
		if retErr != nil {
			// Best-effort cleanup; ignore secondary error.
			_ = os.RemoveAll(infraDir)
		}
	}()

	const templatesRoot = "templates"
	tfs := synthesis.TemplatesFS()

	var artifacts []ejectArtifact
	err := fs.WalkDir(tfs, templatesRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == templatesRoot {
			return nil
		}
		rel, err := filepath.Rel(templatesRoot, p)
		if err != nil {
			return err
		}
		// embed.FS always returns forward slashes; normalize for the OS.
		dst := filepath.Join(infraDir, filepath.FromSlash(rel))

		if d.IsDir() {
			//nolint:gosec // G301: ejected infra/ subdirectories must remain readable/traversable
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			return nil
		}

		if filepath.Base(p) == "main.arm.json" {
			return nil
		}

		data, err := fs.ReadFile(tfs, p)
		if err != nil {
			return err
		}
		//nolint:gosec // G306: ejected Bicep sources are intended to be human-readable
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		artifacts = append(artifacts, ejectArtifact{
			relPath: filepath.ToSlash(filepath.Join("infra", rel)),
			bytes:   len(data),
		})
		return nil
	})
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write infra templates: %s", err),
		)
	}

	return artifacts, nil
}

// writeParametersFile emits infra/main.parameters.json in the standard ARM
// parameter file shape. Only synthesizer-known values (`deployments`,
// `includeAcr`) are written; deploy-time parameters (foundryProjectName,
// location, resourceGroupName, principalId, resourceTokenSalt, tags) are
// supplied by the provider at `azd provision`. The result is a partial
// parameters file -- enough for `bicep build` to validate, not for a
// standalone `az deployment sub create`.
func writeParametersFile(infraDir string, params map[string]any) (ejectArtifact, error) {
	type paramValue struct {
		Value any `json:"value"`
	}
	wrapped := map[string]paramValue{}
	for k, v := range params {
		wrapped[k] = paramValue{Value: v}
	}

	doc := map[string]any{
		"$schema": "https://schema.management.azure.com/" +
			"schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters":     wrapped,
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("marshal main.parameters.json: %s", err),
		)
	}
	// json.MarshalIndent omits a trailing newline; add one for editors/POSIX tools.
	data = append(data, '\n')

	dst := filepath.Join(infraDir, "main.parameters.json")
	//nolint:gosec // G306: ejected parameters file is intended to be human-readable
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write main.parameters.json: %s", err),
		)
	}
	return ejectArtifact{
		relPath: "infra/main.parameters.json",
		bytes:   len(data),
	}, nil
}

// printEjectSummary renders the user-facing success block to stdout.
func printEjectSummary(written []ejectArtifact) {
	fmt.Println()
	fmt.Println("Generating infrastructure files from azure.yaml...")
	fmt.Println()
	for _, a := range written {
		fmt.Printf("  %s %s\n", color.GreenString("Created"), a.relPath)
	}
	fmt.Println()
	fmt.Println("Future provisions will read from ./infra/.")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  azd provision    Apply changes")
	fmt.Println()
}
