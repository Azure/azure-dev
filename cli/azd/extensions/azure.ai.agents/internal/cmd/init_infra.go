// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
// standalone-eject branch would silently drop. `--infra` on a project that
// already declares a Foundry service runs eject only; honoring a positional
// path or an explicitly changed init flag would falsely imply the input was
// acted upon.
//
// Scoped to that branch only: on the init fall-through (a project without a
// Foundry service, or no project at all) the same inputs are accepted, because
// there they genuinely drive init.
func validateStandaloneEjectArgs(cmd *cobra.Command, args []string) error {
	conflicts := standaloneEjectConflictingInputs(cmd, args)
	if len(conflicts) == 0 {
		return nil
	}

	return exterrors.Validation(
		exterrors.CodeInfraEjectConflictingArguments,
		"`--infra` on a project that already declares a Foundry service runs eject only "+
			fmt.Sprintf("and cannot use these init inputs: %s", strings.Join(conflicts, ", ")),
		"drop the init inputs and run `azd ai agent init --infra` from the project root, "+
			"or remove --infra to run the normal init flow",
	)
}

// ensureDefaultInfraModule refuses a custom infra.module. Eject writes
// main.bicep/main.parameters.json or main.tfvars.json, while azd derives those
// filenames from infra.module and would ignore the generated entry point or
// parameter file.
func ensureDefaultInfraModule(rawYAML []byte) error {
	config, err := readDeclaredInfraConfig(rawYAML)
	if err != nil {
		return err
	}

	if config.Module == "" || config.Module == "main" || config.Module == "./main" {
		return nil
	}

	return exterrors.Validation(
		exterrors.CodeInfraEjectCustomModule,
		fmt.Sprintf("azure.yaml selects infrastructure module %q via `infra.module`; `--infra` "+
			"generates the default main module and cannot preserve that entry point", config.Module),
		fmt.Sprintf("run `azd ai agent init` without --infra to keep module %q, or remove "+
			"`infra.module` first if you want the generated main module to replace it", config.Module),
	)
}

// standaloneEjectConflictingInputs returns every changed input the standalone
// eject branch cannot honor. Global execution controls remain valid; everything
// else is assumed to belong to init so newly-added flags fail safe instead of
// being silently ignored.
func standaloneEjectConflictingInputs(cmd *cobra.Command, args []string) []string {
	allowed := map[string]struct{}{
		"cwd":            {},
		"debug":          {},
		"infra":          {},
		"no-prompt":      {},
		"output":         {},
		"trace-log-file": {},
		"trace-log-url":  {},
	}
	seen := map[string]struct{}{}
	if len(args) > 0 {
		seen["positional path"] = struct{}{}
	}

	collect := func(flags *pflag.FlagSet) {
		flags.Visit(func(flag *pflag.Flag) {
			if _, ok := allowed[flag.Name]; ok {
				return
			}
			seen["--"+flag.Name] = struct{}{}
		})
	}
	collect(cmd.Flags())
	collect(cmd.InheritedFlags())

	conflicts := slices.Collect(maps.Keys(seen))
	slices.Sort(conflicts)
	return conflicts
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

// readProjectAzureYAML reads azure.yaml from projectRoot, reporting a missing
// file as the structured CodeInfraEjectAzureYamlMissing refusal so every eject
// entry point surfaces the same code for the same condition.
func readProjectAzureYAML(projectRoot string) ([]byte, error) {
	//nolint:gosec // G304: azure.yaml under the caller-supplied azd project root
	raw, err := os.ReadFile(filepath.Join(projectRoot, "azure.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, exterrors.Validation(
				exterrors.CodeInfraEjectAzureYamlMissing,
				"azure.yaml not found in the current directory; "+
					"`azd ai agent init --infra` requires an existing azd agent project",
				"run `azd ai agent init` first to create azure.yaml, then re-run with --infra",
			)
		}
		return nil, fmt.Errorf("read azure.yaml: %w", err)
	}

	return raw, nil
}

// defaultInfraDirName is the directory eject writes into, and the directory an
// azd project uses when it does not declare `infra.path`.
const defaultInfraDirName = "infra"

// hasFoundryServiceForEject reports whether azure.yaml at projectRoot already
// declares the Foundry provisioning service that eject synthesizes from.
//
// "No Foundry service" is reported as (false, nil) rather than an error: it
// means the project simply has nothing to eject yet, which callers resolve by
// running the normal init flow first. Malformed YAML and ambiguous projects
// (several Foundry services) still surface as errors.
func hasFoundryServiceForEject(projectRoot string) (bool, error) {
	rawYAML, err := readProjectAzureYAML(projectRoot)
	if err != nil {
		return false, err
	}

	return hasFoundryServiceInYAML(rawYAML)
}

// hasFoundryServiceInYAML is hasFoundryServiceForEject for callers that have
// already read azure.yaml and need it for more than the service scan.
func hasFoundryServiceInYAML(rawYAML []byte) (bool, error) {
	if _, err := findFoundryServiceForEject(rawYAML); err != nil {
		if localErr, ok := errors.AsType[*azdext.LocalError](err); ok &&
			localErr.Code == exterrors.CodeInfraEjectNoFoundryService {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

type declaredInfraConfig struct {
	Provider string `yaml:"provider"`
	Path     string `yaml:"path"`
	Module   string `yaml:"module"`
	Layers   []struct {
		Name     string `yaml:"name"`
		Provider string `yaml:"provider"`
		Path     string `yaml:"path"`
	} `yaml:"layers"`
}

// readDeclaredInfraConfig returns the infra configuration declared in
// azure.yaml. Type-invalid infra fields use the same invalid-azure.yaml
// classification as the service scan; otherwise a value such as
// `infra.layers: unexpected` would look like no layers and bypass the guard.
func readDeclaredInfraConfig(rawYAML []byte) (declaredInfraConfig, error) {
	var doc struct {
		Infra declaredInfraConfig `yaml:"infra"`
	}
	if err := yaml.Unmarshal(rawYAML, &doc); err != nil {
		return declaredInfraConfig{}, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}

	return doc.Infra, nil
}

// normalizeInfraPath mirrors azd core's project-path normalization before
// comparing a declared path with the default ./infra directory.
func normalizeInfraPath(path string) string {
	if strings.Contains(path, "\\") && !strings.Contains(path, "/") {
		path = strings.ReplaceAll(path, "\\", "/")
	}
	return filepath.Clean(filepath.FromSlash(path))
}

// ensureDefaultInfraPath refuses when azure.yaml points the project's
// infrastructure somewhere other than ./infra/ via `infra.path`.
//
// Eject always writes ./infra/, and the Terraform path additionally stamps
// `infra.provider: terraform` and drops `infra.path` so azd-core provisions the
// generated module. On a project that declares its own `infra.path`, that
// combination aims provisioning at the Foundry-only template and leaves the
// directory the user actually maintains orphaned on disk.
//
// The refusal is about the declaration, not the directory: ./infra/ being absent
// proves nothing when the project's IaC deliberately lives elsewhere, so
// ensureInfraDirAbsent alone would wave this project through.
func ensureDefaultInfraPath(rawYAML []byte) error {
	config, err := readDeclaredInfraConfig(rawYAML)
	if err != nil {
		return err
	}

	declared := config.Path
	if declared == "" || normalizeInfraPath(declared) == defaultInfraDirName {
		return nil
	}

	return exterrors.Validation(
		exterrors.CodeInfraEjectCustomInfraPath,
		fmt.Sprintf("azure.yaml points this project's infrastructure at %q via `infra.path`; "+
			"`--infra` writes a self-contained Foundry template to ./infra/ and cannot take "+
			"over infrastructure the project already owns", declared),
		fmt.Sprintf("run `azd ai agent init` without --infra to add the agent while %q stays the "+
			"project's infrastructure, or remove `infra.path` from azure.yaml first if you want "+
			"the generated ./infra/ to replace it", declared),
	)
}

// ensureNoInfraLayers refuses projects that use infra.layers. Eject generates
// one self-contained Foundry module under ./infra and cannot preserve layer
// paths, dependency ordering, hooks, or provider inheritance.
func ensureNoInfraLayers(rawYAML []byte) error {
	config, err := readDeclaredInfraConfig(rawYAML)
	if err != nil {
		return err
	}
	if len(config.Layers) == 0 {
		return nil
	}

	return exterrors.Validation(
		exterrors.CodeInfraEjectLayersUnsupported,
		fmt.Sprintf("azure.yaml declares %d infrastructure layer(s); `--infra` generates one "+
			"self-contained Foundry template and cannot preserve layered provisioning", len(config.Layers)),
		"run `azd ai agent init` without --infra to keep the existing layers, or remove "+
			"`infra.layers` first if you want the generated ./infra/ template to replace them",
	)
}

// ensureCompatibleInfraProvider refuses a Bicep eject when azure.yaml selects a
// different provisioning provider. Bicep eject intentionally leaves
// infra.provider unchanged, so azd would continue dispatching to that provider
// and ignore the generated files.
func ensureCompatibleInfraProvider(rawYAML []byte, requestedProvider string) error {
	config, err := readDeclaredInfraConfig(rawYAML)
	if err != nil {
		return err
	}

	if requestedProvider != project.BicepProviderName {
		return nil
	}

	declared := config.Provider
	if declared == "" ||
		declared == project.BicepProviderName ||
		declared == project.FoundryProviderName {
		return nil
	}

	suggestion := "change or remove `infra.provider` before ejecting Bicep"
	if declared == project.TerraformProviderName {
		suggestion = "use `azd ai agent init --infra=terraform` to generate Terraform, " +
			"or change/remove `infra.provider` before ejecting Bicep"
	}

	return exterrors.Validation(
		exterrors.CodeInfraEjectProviderConflict,
		fmt.Sprintf("azure.yaml uses `infra.provider: %s`, but `--infra=bicep` generates Bicep "+
			"without changing the provider; azd would continue using %s and ignore the generated files",
			declared, declared),
		suggestion,
	)
}

// ensureInfraDirAbsent refuses when projectRoot already contains ./infra/.
// Eject writes the whole tree or nothing, so it never merges into or overwrites
// what is already there.
//
// Eject leaves no marker behind, so nothing at this point can tell prior eject
// output apart from infrastructure the user authored for the project's other
// services — a plain "delete ./infra/" would be destructive advice half the
// time. The suggestion therefore covers both cases and leads with the
// non-destructive one.
func ensureInfraDirAbsent(projectRoot string) error {
	// Lstat makes any directory entry count, including a dangling symlink.
	// Eject must never replace a user-owned path whose target happens to be
	// missing.
	if _, err := os.Lstat(filepath.Join(projectRoot, defaultInfraDirName)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat infra directory: %w", err)
	}

	return exterrors.Validation(
		exterrors.CodeInfraEjectExists,
		"`./infra/` already exists",
		"if you authored ./infra/, keep it and run `azd ai agent init` without --infra: "+
			"--infra synthesizes a self-contained Foundry template and cannot merge into "+
			"infrastructure you already own. If a previous --infra run generated it, "+
			"delete ./infra/ and run the command again to regenerate it from azure.yaml",
	)
}

// infraGate is how `azd ai agent init --infra` should behave for the azd
// project (if any) that contains the current directory.
type infraGate struct {
	// standaloneEject is true when an existing project already declares a
	// Foundry service, so eject runs on its own and skips the init prompts.
	standaloneEject bool
	// projectRoot is the resolved azd project root. Empty when the current
	// directory is not inside a project yet.
	projectRoot string
}

// resolveInfraGate decides between a standalone eject and the normal init flow
// for a `--infra` run.
//
// A project that already declares a Foundry service ejects standalone. Anything
// else — no project at all, or a project azd already manages that has no Foundry
// service yet — runs init first and ejects afterwards, so "add an agent to my
// existing project and give me its IaC" stays a single step instead of failing
// with "nothing to eject".
//
// The one refusal kept up front is a pre-existing ./infra/: init cannot clear
// it, so failing here beats mutating azure.yaml and then refusing on the
// trailing eject. Projects that use a custom infra.path, infra.layers, or an
// incompatible provider are refused on both branches for the same reason.
func resolveInfraGate(provider string) (infraGate, error) {
	projectRoot, err := azdext.GetProjectDir()
	if errors.Is(err, azdext.ErrProjectNotFound) {
		return infraGate{}, nil
	}
	if err != nil {
		return infraGate{}, fmt.Errorf("resolve azd project directory: %w", err)
	}

	rawYAML, err := readProjectAzureYAML(projectRoot)
	if err != nil {
		return infraGate{}, err
	}

	// Scanned before the infra.path check so a malformed azure.yaml keeps its
	// established CodeInvalidAzureYaml classification.
	hasFoundry, err := hasFoundryServiceInYAML(rawYAML)
	if err != nil {
		return infraGate{}, err
	}

	if err := ensureNoInfraLayers(rawYAML); err != nil {
		return infraGate{}, err
	}
	if err := ensureDefaultInfraPath(rawYAML); err != nil {
		return infraGate{}, err
	}
	if err := ensureDefaultInfraModule(rawYAML); err != nil {
		return infraGate{}, err
	}
	if err := ensureCompatibleInfraProvider(rawYAML, provider); err != nil {
		return infraGate{}, err
	}

	if hasFoundry {
		return infraGate{standaloneEject: true, projectRoot: projectRoot}, nil
	}

	// The trailing eject writes ./infra/, and init cannot clear a directory that
	// is already there. Refuse now rather than mutating azure.yaml and adding an
	// azd environment first and only then refusing.
	if err := ensureInfraDirAbsent(projectRoot); err != nil {
		return infraGate{}, err
	}

	return infraGate{projectRoot: projectRoot}, nil
}

// ejectInfraAfterInit ejects from the azd project containing the current
// directory. Init may create or discover a project above cwd, so use the same
// upward project resolution as the rest of azd.
func ejectInfraAfterInit(provider string) error {
	if provider == "" {
		return nil
	}

	projectRoot, err := azdext.GetProjectDir()
	if errors.Is(err, azdext.ErrProjectNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve azd project directory after init: %w", err)
	}

	hasFoundry, err := hasFoundryServiceForEject(projectRoot)
	if err != nil {
		return err
	}
	if !hasFoundry {
		return nil
	}

	return ejectInfra(projectRoot, provider)
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
//   - azure.yaml declares infra.layers -> CodeInfraEjectLayersUnsupported
//   - azure.yaml declares a non-default infra.path -> CodeInfraEjectCustomInfraPath
//   - azure.yaml declares a non-default infra.module -> CodeInfraEjectCustomModule
//   - Bicep eject conflicts with infra.provider -> CodeInfraEjectProviderConflict
//   - ./infra/ already exists -> CodeInfraEjectExists
//
// On success it prints the summary block and returns nil.
func ejectInfra(projectRoot, provider string) error {
	rawYAML, err := readProjectAzureYAML(projectRoot)
	if err != nil {
		return err
	}

	svcName, err := findFoundryServiceForEject(rawYAML)
	if err != nil {
		return err
	}

	// Checked before anything is written: eject cannot preserve layers, the
	// Terraform path drops infra.path, and Bicep leaves an existing provider
	// unchanged. Any of those could make azd ignore or orphan user-owned IaC.
	if err := ensureNoInfraLayers(rawYAML); err != nil {
		return err
	}
	if err := ensureDefaultInfraPath(rawYAML); err != nil {
		return err
	}
	if err := ensureDefaultInfraModule(rawYAML); err != nil {
		return err
	}
	if err := ensureCompatibleInfraProvider(rawYAML, provider); err != nil {
		return err
	}

	infraDir := filepath.Join(projectRoot, defaultInfraDirName)
	if err := ensureInfraDirAbsent(projectRoot); err != nil {
		return err
	}

	res, err := synthesis.Synthesize(synthesis.Input{
		RawAzureYAML:  rawYAML,
		ServiceName:   svcName,
		AcceptedHosts: project.FoundryProvisioningServiceHosts,
		ProjectRoot:   projectRoot,
		// Eject writes a static infra/ tree. Keep ${VAR} references verbatim so
		// the ejected main.parameters.json stays environment-portable; the
		// on-disk provision flow resolves them from the azd environment.
		PreserveVarRefs: true,
	})
	if err != nil {
		// A brownfield (endpoint:) project provisions through the extension's
		// brownfield path, which never compiles ./infra/. Ejecting IaC for it
		// would be misleading, so refuse with a clear message instead of the
		// raw synthesizer error.
		if errors.Is(err, synthesis.ErrEndpointBrownfield) {
			return exterrors.Validation(
				exterrors.CodeInfraEjectBrownfieldUnsupported,
				"`azd ai agent init --infra` is not supported for a project that reuses an existing "+
					"Foundry resource (the azure.ai.project service sets endpoint:)",
				"remove --infra: the extension provisions the existing project (and any required "+
					"container registry) directly with `azd provision`",
			)
		}
		// Reuse the provider's vocabulary so eject and provision report
		// consistent codes for the same azure.yaml problems.
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("synthesize foundry project service %q: %s", svcName, err),
			"check the endpoint, deployments, and network fields under your azure.ai.project service",
		)
	}

	if provider == project.TerraformProviderName {
		// Private networking is Bicep-only today: the Terraform module has no
		// VNet / private-endpoint / DNS / networkInjections resources, so ejecting
		// it for a network: service would silently drop the config and provision a
		// public account — the exact silent public fallback the network: contract
		// forbids. Fail fast instead of ejecting an insecure template.
		if res.NetworkMode != synthesis.NetworkModeNone {
			return exterrors.Validation(
				exterrors.CodeInfraEjectNetworkUnsupported,
				"private networking (the service's network: block) is not yet supported with Terraform",
				"eject Bicep instead with `azd ai agent init --infra` (or `--infra=bicep`), "+
					"or remove the network: block to provision a public account with Terraform",
			)
		}
	}

	// Eject writes the whole infra/ tree or none of it: if any writer fails
	// after MkdirAll, remove the partial directory so the next run isn't blocked
	// by the "./infra/ already exists" refuse above and the command stays
	// retryable without manual cleanup.
	var ejectErr error
	if provider == project.TerraformProviderName {
		ejectErr = ejectTerraform(projectRoot, infraDir, res.Parameters)
	} else {
		ejectErr = ejectBicep(infraDir, res.Parameters)
	}
	if ejectErr != nil {
		_ = os.RemoveAll(infraDir)
	}
	return ejectErr
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

// findFoundryServiceForEject scans azure.yaml for the azure.ai.project service
// and returns its name, using eject-specific error codes so telemetry can
// distinguish init-time eject from provision failures.
func findFoundryServiceForEject(raw []byte) (string, error) {
	type svc struct {
		Host    string    `yaml:"host"`
		Network yaml.Node `yaml:"network,omitempty"`
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
	var misplacedNetwork []string
	for name, s := range r.Services {
		if slices.Contains(project.FoundryProjectServiceHosts, s.Host) {
			matches = append(matches, name)
			continue
		}
		if project.IsFoundryNetworkHost(s.Host) && !s.Network.IsZero() {
			misplacedNetwork = append(misplacedNetwork, name)
		}
	}
	if len(misplacedNetwork) > 0 {
		slices.Sort(misplacedNetwork)
		return "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("network: is only supported on services with host: %s (found on %v)",
				project.FoundryProjectHost, misplacedNetwork),
			"move the network: block to the azure.ai.project service (for example, services.ai-project)",
		)
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		var legacyMatches []string
		for name, s := range r.Services {
			if slices.Contains(project.FoundryLegacyProvisioningHosts, s.Host) {
				legacyMatches = append(legacyMatches, name)
			}
		}
		switch len(legacyMatches) {
		case 1:
			return legacyMatches[0], nil
		case 0:
			return "", exterrors.Dependency(
				exterrors.CodeInfraEjectNoFoundryService,
				fmt.Sprintf("no foundry provisioning service found in azure.yaml (looking for host in %v); "+
					"nothing to eject", project.FoundryProvisioningServiceHosts),
				fmt.Sprintf("add a service with `host: %s` to azure.yaml, "+
					"or remove --infra to run init normally", project.FoundryProjectHost),
			)
		default:
			slices.Sort(legacyMatches)
			return "", exterrors.Dependency(
				exterrors.CodeInfraEjectMultipleFoundryServices,
				fmt.Sprintf("multiple legacy services declare a foundry provisioning host %v (%v); only one is supported",
					project.FoundryLegacyProvisioningHosts, legacyMatches),
				"keep a single azure.ai.project service per project, or a single pre-split foundry service",
			)
		}
	default:
		// Sort for deterministic error message; map iteration order is
		// randomized and would otherwise produce flaky tests.
		slices.Sort(matches)
		return "", exterrors.Dependency(
			exterrors.CodeInfraEjectMultipleFoundryServices,
			fmt.Sprintf("multiple services declare a foundry project host %v (%v); only one is supported",
				project.FoundryProjectServiceHosts, matches),
			"keep a single azure.ai.project service per project",
		)
	}
}

// writeEmbeddedTemplates copies every file under the synthesizer's embedded
// templates/ root into infraDir, preserving the relative tree, and returns the
// files written (with sizes). On any error it removes the partial infraDir.
//
// Three files are skipped:
//   - main.arm.json (the pre-compiled ARM JSON): would be stale once the user
//     edits main.bicep.
//   - brownfield.bicep and brownfield.arm.json: unreachable in a greenfield
//     eject. ejectInfra already refuses to eject a brownfield (endpoint:)
//     project, main.bicep never references brownfield.bicep, and the
//     provider's brownfield path always loads the embedded
//     synthesis.BrownfieldARMTemplate() instead of anything under infra/.
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

		switch filepath.Base(p) {
		case "main.arm.json", "brownfield.bicep", "brownfield.arm.json":
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
// environment at provision time. The synthesizer-known values `deployments`
// and `connections` are written literally; deploy-time inputs (location,
// resource_group_name, foundry_project_name, principal_id, subscription_id,
// environment_name, resource_token_salt) are left as ${AZURE_*} placeholders.
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

	// deployments and connections are the only synthesizer-derived values
	// written to tfvars. The Terraform provider resolves ${VAR} references
	// across the generated file at provision time.
	if v, ok := params["deployments"]; ok {
		doc["deployments"] = v
	}
	connections, ok := params["connections"].([]synthesis.Connection)
	if !ok {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("connections parameter has unexpected type %T", params["connections"]),
		)
	}
	connectionCredentials, ok := params["connectionCredentials"].(map[string]map[string]any)
	if !ok {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf(
				"connectionCredentials parameter has unexpected type %T",
				params["connectionCredentials"],
			),
		)
	}
	doc["connections"] = synthesis.JoinConnectionCredentials(
		connections,
		connectionCredentials,
	)

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
