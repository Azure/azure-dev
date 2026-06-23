// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

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

// parseInfraProvider normalizes the --infra flag value into a supported
// provider name. A bare `--infra` arrives as "bicep" (the flag's NoOptDefVal),
// so the accepted values are "bicep" and "terraform" (case-insensitive). The
// caller only invokes this when the flag was set (flags.infra != "").
func parseInfraProvider(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case project.BicepProviderName:
		return project.BicepProviderName, nil
	case project.TerraformProviderName:
		return project.TerraformProviderName, nil
	default:
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unsupported --infra value %q", value),
			"pass --infra=bicep or --infra=terraform (a bare --infra ejects Bicep)",
		)
	}
}

// ejectInfra synthesizes the embedded Bicep templates from azure.yaml and
// ejectInfra synthesizes infrastructure templates from azure.yaml and writes
// them into projectRoot/infra/. Invoked by `azd ai agent init --infra[=<provider>]`
// either after a fresh init or as a standalone eject on an existing project.
//
// provider selects the IaC flavor:
//
//   - "bicep": copies the embedded Bicep tree + main.parameters.json. azure.yaml
//     is NOT modified (the microsoft.foundry provider compiles the on-disk Bicep).
//   - "terraform": copies the embedded .tf module + a generated main.tfvars.json,
//     then stamps `infra.provider: terraform` so azd-core's built-in Terraform
//     provider handles provisioning. This is the one path that mutates azure.yaml.
//
// Refuse conditions (provider-independent):
//
//   - azure.yaml is missing -> CodeInfraEjectAzureYamlMissing
//   - no service has a Foundry host -> CodeInfraEjectNoFoundryService
//   - ./infra/ already exists -> CodeInfraEjectExists
//
// On success it prints the summary block and returns nil.
func ejectInfra(projectRoot, provider string) error {
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
		// Eject writes a static infra/ tree. Keep ${VAR} references verbatim so
		// the ejected main.parameters.json stays environment-portable; the
		// on-disk provision flow resolves them from the azd environment.
		PreserveVarRefs: true,
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

	if provider == project.TerraformProviderName {
		return ejectTerraform(projectRoot, infraDir, res.Parameters)
	}
	return ejectBicep(infraDir, res.Parameters)
}

// ejectBicep writes the embedded Bicep tree plus the synthesized
// main.parameters.json into infraDir and prints the summary. It does not
// modify azure.yaml; the declared infra.provider is left unchanged.
func ejectBicep(infraDir string, params map[string]any) error {
	written, err := writeEmbeddedTemplates(infraDir)
	if err != nil {
		return err
	}

	paramsArtifact, err := writeParametersFile(infraDir, params)
	if err != nil {
		return err
	}
	written = append(written, paramsArtifact)
	slices.SortFunc(written, func(a, b ejectArtifact) int {
		return strings.Compare(a.relPath, b.relPath)
	})

	printEjectSummary(written, project.BicepProviderName)
	return nil
}

// ejectTerraform writes the embedded Terraform module plus the generated
// main.tfvars.json into infraDir, stamps `infra.provider: terraform` onto
// azure.yaml so azd-core's Terraform provider takes over provisioning, and
// prints the summary.
//
// acr.tf is written only when an agent uses docker: (includeAcr). outputs.tf is
// generated to match: the ACR outputs are included only when acr.tf is present,
// and omitted entirely otherwise.
func ejectTerraform(projectRoot, infraDir string, params map[string]any) error {
	includeAcr, _ := params["includeAcr"].(bool)

	written, err := writeEmbeddedTerraformTemplates(infraDir, includeAcr)
	if err != nil {
		return err
	}

	outputsArtifact, err := writeOutputsFile(infraDir, includeAcr)
	if err != nil {
		return err
	}
	written = append(written, outputsArtifact)

	tfvarsArtifact, err := writeTfvarsFile(infraDir, params)
	if err != nil {
		return err
	}
	written = append(written, tfvarsArtifact)

	// Stamp the provider so `azd provision` dispatches to azd-core's Terraform
	// provider instead of this extension's microsoft.foundry provider. Done
	// after the files land so a stamp failure does not leave azure.yaml
	// pointing at an infra/ that was never written.
	if err := stampInfraProvider(projectRoot, project.TerraformProviderName); err != nil {
		// Best-effort cleanup so a half-ejected project isn't left behind.
		_ = os.RemoveAll(infraDir)
		return err
	}

	slices.SortFunc(written, func(a, b ejectArtifact) int {
		return strings.Compare(a.relPath, b.relPath)
	})

	printEjectSummary(written, project.TerraformProviderName)
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

// writeEmbeddedTerraformTemplates copies the static *.tf files under the
// embedded templates/terraform/ root into infraDir (flat -- the module has no
// submodules) and returns the files written. On any error it removes the
// partial infraDir.
//
// acr.tf is copied only when includeAcr is true (an agent uses docker:);
// otherwise it is omitted and outputs.tf carries no ACR outputs.
//
// Files that are not verbatim copies are skipped here and produced elsewhere:
// outputs.tf is rendered from outputs.tf.tmpl by writeOutputsFile, and
// main.tfvars.json is generated by writeTfvarsFile, so neither goes stale.
func writeEmbeddedTerraformTemplates(infraDir string, includeAcr bool) (_ []ejectArtifact, retErr error) {
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

	const templatesRoot = "templates/terraform"
	tfs := synthesis.TerraformTemplatesFS()

	entries, err := fs.ReadDir(tfs, templatesRoot)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("read terraform templates: %s", err),
		)
	}

	var artifacts []ejectArtifact
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Copy only verbatim .tf files; outputs.tf.tmpl (and any other non-.tf
		// file) is rendered/generated elsewhere.
		if !strings.HasSuffix(name, ".tf") {
			continue
		}
		// acr.tf is omitted unless an agent uses docker:.
		if name == "acr.tf" && !includeAcr {
			continue
		}
		data, err := fs.ReadFile(tfs, templatesRoot+"/"+name)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("read terraform template %s: %s", name, err),
			)
		}
		//nolint:gosec // G306: ejected Terraform sources are intended to be human-readable
		if err := os.WriteFile(filepath.Join(infraDir, name), data, 0o644); err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("write terraform template %s: %s", name, err),
			)
		}
		artifacts = append(artifacts, ejectArtifact{
			relPath: filepath.ToSlash(filepath.Join("infra", name)),
			bytes:   len(data),
		})
	}

	return artifacts, nil
}

// writeOutputsFile renders infra/outputs.tf from the embedded outputs.tf.tmpl.
// The ACR outputs are included only when includeAcr is true (acr.tf was
// written); otherwise they are omitted entirely, since Terraform resolves
// resource references statically and acr.tf's resources are not present.
func writeOutputsFile(infraDir string, includeAcr bool) (ejectArtifact, error) {
	const tmplPath = "templates/terraform/outputs.tf.tmpl"
	raw, err := fs.ReadFile(synthesis.TerraformTemplatesFS(), tmplPath)
	if err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("read outputs template: %s", err),
		)
	}

	tmpl, err := template.New("outputs.tf").Parse(string(raw))
	if err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("parse outputs template: %s", err),
		)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, struct{ IncludeAcr bool }{IncludeAcr: includeAcr}); err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("render outputs template: %s", err),
		)
	}

	dst := filepath.Join(infraDir, "outputs.tf")
	//nolint:gosec // G306: ejected Terraform sources are intended to be human-readable
	if err := os.WriteFile(dst, buf.Bytes(), 0o644); err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write outputs.tf: %s", err),
		)
	}
	return ejectArtifact{
		relPath: "infra/outputs.tf",
		bytes:   buf.Len(),
	}, nil
}

// writeTfvarsFile emits infra/main.tfvars.json. azd-core's Terraform provider
// reads this file and substitutes the ${...} placeholders from the azd
// environment at provision time. The synthesizer-known value `deployments` is
// written literally; deploy-time inputs (location, resource_group_name,
// foundry_project_name, principal_id, subscription_id, environment_name,
// resource_token_salt) are left as ${AZURE_*} placeholders.
//
// include_acr is NOT written: whether ACR is provisioned is decided at eject
// time by the presence of acr.tf, not by a Terraform variable.
func writeTfvarsFile(infraDir string, params map[string]any) (ejectArtifact, error) {
	// Static keys carry ${...} placeholders azd resolves from the environment.
	// json.MarshalIndent sorts map keys alphabetically, so the generated file is
	// deterministic; the placeholder values are JSON strings azd env-substitutes.
	doc := map[string]any{
		"subscription_id":      "${AZURE_SUBSCRIPTION_ID}",
		"location":             "${AZURE_LOCATION}",
		"resource_group_name":  "${AZURE_RESOURCE_GROUP}",
		"environment_name":     "${AZURE_ENV_NAME}",
		"foundry_project_name": "${AZURE_AI_PROJECT_NAME}",
		"principal_id":         "${AZURE_PRINCIPAL_ID}",
		"resource_token_salt":  "${AZURE_RESOURCE_TOKEN_SALT}",
	}

	// deployments is the only synthesizer-derived value written to tfvars.
	if v, ok := params["deployments"]; ok {
		doc["deployments"] = v
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("marshal main.tfvars.json: %s", err),
		)
	}
	// json.MarshalIndent omits a trailing newline; add one for editors/POSIX tools.
	data = append(data, '\n')

	dst := filepath.Join(infraDir, "main.tfvars.json")
	//nolint:gosec // G306: ejected tfvars file is intended to be human-readable
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write main.tfvars.json: %s", err),
		)
	}
	return ejectArtifact{
		relPath: "infra/main.tfvars.json",
		bytes:   len(data),
	}, nil
}

// stampInfraProvider sets `infra.provider: <provider>` in azure.yaml, creating
// the infra: block if absent and dropping any starter `infra.path`. Eject runs
// as a standalone command without an AzdClient, so this is an in-place YAML
// edit (the Bicep path leaves azure.yaml untouched; only Terraform stamps a
// provider so azd-core takes over provisioning).
func stampInfraProvider(projectRoot, provider string) error {
	yamlPath := filepath.Join(projectRoot, "azure.yaml")
	//nolint:gosec // G304: azure.yaml under the caller-supplied azd project root
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return fmt.Errorf("read azure.yaml for provider stamp: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"azure.yaml is not a YAML mapping at the top level",
			"verify azure.yaml is a valid azd project file",
		)
	}

	doc := root.Content[0]
	infra := mappingValue(doc, "infra")
	if infra == nil {
		infra = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "infra"},
			infra,
		)
	}
	setMappingScalar(infra, "provider", provider)
	removeMappingKey(infra, "path")

	out, err := yaml.Marshal(&root)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("marshal azure.yaml after provider stamp: %s", err),
		)
	}
	//nolint:gosec // G306: azure.yaml is a human-edited project file
	if err := os.WriteFile(yamlPath, out, 0o644); err != nil {
		return exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write azure.yaml after provider stamp: %s", err),
		)
	}
	return nil
}

// mappingValue returns the value node for key in a YAML mapping node, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// setMappingScalar sets key to a scalar string value in a YAML mapping node,
// updating the existing value node in place when present (preserving order).
func setMappingScalar(m *yaml.Node, key, value string) {
	if v := mappingValue(m, key); v != nil {
		v.Kind = yaml.ScalarNode
		v.Tag = "!!str"
		v.Value = value
		return
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

// removeMappingKey deletes a key (and its value) from a YAML mapping node.
func removeMappingKey(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// printEjectSummary renders the user-facing success block to stdout. For the
// Terraform provider it also notes that infra.provider was set in azure.yaml.
func printEjectSummary(written []ejectArtifact, provider string) {
	fmt.Println()
	fmt.Println("Generating infrastructure files from azure.yaml...")
	fmt.Println()
	for _, a := range written {
		fmt.Printf("  %s %s\n", color.GreenString("Created"), a.relPath)
	}
	fmt.Println()
	if provider == project.TerraformProviderName {
		fmt.Printf("  %s azure.yaml (infra.provider: terraform)\n", color.GreenString("Updated"))
		fmt.Println()
	}
	fmt.Println("Future provisions will read from ./infra/.")
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  azd provision    Apply changes")
	fmt.Println()
}
