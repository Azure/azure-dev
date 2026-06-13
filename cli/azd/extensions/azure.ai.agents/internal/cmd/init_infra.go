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
// stable across operating systems and matches the spec's example.
type ejectArtifact struct {
	relPath string // e.g. "infra/main.bicep"
	bytes   int    // size of the file just written
}

// validateStandaloneEjectArgs refuses init-driving inputs that would be
// silently dropped by the standalone-eject branch. Per the spec, `--infra`
// on an existing project runs eject only -- it does not re-prompt, scaffold
// agent code, or add a service. Honoring a positional path, `-m`, or
// `--src` here would create the false impression that the input was acted
// upon; returning a structured error is more honest.
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
// writes them into projectRoot/infra/. The function is invoked by `azd ai
// agent init --infra` in two contexts:
//
//  1. A fresh init that has just produced azure.yaml in projectRoot.
//  2. A standalone eject on an existing Bicep-less azd agent project.
//
// Refuse conditions follow spec/bicepless-foundry/spec.md (§Eject Command):
//
//   - azure.yaml is missing -> CodeInfraEjectAzureYamlMissing
//   - no service in azure.yaml has a Foundry host kind -> CodeInfraEjectNoFoundryService
//   - ./infra/ already exists (even empty) -> CodeInfraEjectExists
//
// On success the function prints the spec's success block and returns nil.
// The function does NOT modify azure.yaml (the spec is explicit that
// infra.provider stays azure.ai.agents).
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
		// Surface validation errors that the synthesizer itself produced.
		// The provider has its own classification at provision time; for
		// eject we keep the same vocabulary so users see consistent codes.
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
// project.FoundryServiceHosts and returns its name. Unlike the provider's
// internal findFoundryService, this version returns eject-specific error
// codes so telemetry can distinguish init-time eject failures from
// provision-time provider failures.
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
// templates/ root into infraDir, preserving the relative tree. Returns the
// list of files actually written (each with its on-disk size) so the caller
// can render the "Created infra/..." lines.
//
// The function creates infraDir (and any required subdirectories) with 0o755
// and writes files with 0o644. On any error mid-walk it removes the partial
// infraDir to avoid leaving the project in a half-ejected state.
//
// main.arm.json (the pre-compiled ARM JSON shipped inside the extension for
// the in-memory provisioning path) is deliberately skipped: the point of
// eject is to hand the user the human-readable Bicep sources, and the
// embedded JSON would be stale the moment they edit main.bicep.
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
// parameter file shape. The values come from the synthesizer (today: just
// `deployments` and `includeAcr`); deploy-time parameters such as
// foundryProjectName, location, principalId, resourceTokenSalt, and tags
// are intentionally omitted because they are not known at init time and
// are supplied by the provider at `azd provision`. The file therefore acts
// as a partial parameters file -- enough for `bicep build` to validate the
// template but not enough for a standalone `az deployment group create`.
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
	// json.MarshalIndent omits a trailing newline; add one so the file
	// plays nicely with text editors and POSIX tools.
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

// printEjectSummary renders the user-facing success block to stdout. Format
// follows spec/bicepless-foundry/spec.md (§Eject Command, line 280):
//
//	Generating infrastructure files from azure.yaml...
//
//	  Created infra/<file>
//	  Created infra/<file>
//
//	Future provisions will read from ./infra/.
//
//	Next steps:
//	  azd provision    Apply changes
//
// Per cli/azd/extensions/azure.ai.agents/AGENTS.md, fmt.Print* is the
// user-facing output channel (log is reserved for --debug-only diagnostics).
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
