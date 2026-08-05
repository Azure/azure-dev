// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"azureaiagent/internal/exterrors"
	projectpaths "azureaiagent/internal/pkg/paths"
	"azureaiagent/internal/project"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"go.yaml.in/yaml/v3"
)

const (
	defaultInfraPath       = "infra"
	defaultInfraModule     = "main"
	foundryInfraLayerName  = "foundry"
	foundryInfraLayerPath  = "infra/foundry"
	foundryTerraformMarker = ".azd-foundry"
	foundryTerraformV1     = "terraform-v1\n"
)

// ejectArtifact records one file the eject step produced.
// Paths are forward-slash relative to projectRoot so the success output is
// stable across operating systems.
type ejectArtifact struct {
	relPath string // e.g. "infra/main.bicep"
	bytes   int    // size of the file just written
}

type infraEjectPlan struct {
	targetDir         string
	targetPath        string
	module            string
	layer             bool
	updatedYAML       []byte
	updateDescription string
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

// ejectInfra synthesizes infrastructure templates from azure.yaml. A project
// that already owns infrastructure is migrated to infra.layers and receives a
// dedicated Foundry layer under infra/foundry; a Foundry-only project keeps the
// legacy infra/ layout.
//
// provider selects the IaC flavor:
//
//   - "bicep": azd-core's Bicep provider compiles the on-disk Bicep.
//   - "terraform": azd-core's built-in Terraform provider handles the layer.
//
// Refuse conditions (provider-independent):
//
//   - azure.yaml is missing -> CodeInfraEjectAzureYamlMissing
//   - no service has a Foundry host -> CodeInfraEjectNoFoundryService
//   - a generated destination file already exists -> CodeInfraEjectExists
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
	plan, err := planInfraEject(projectRoot, rawYAML, provider)
	if err != nil {
		return err
	}
	if err := validateEjectTarget(projectRoot, plan); err != nil {
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

	// Generate away from the destination, then install only after every file is
	// ready. This lets a layered eject merge into an existing infra/ tree without
	// exposing partial files or deleting user-owned content on failure.
	stageDir, err := os.MkdirTemp(projectRoot, ".azd-foundry-infra-*")
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("create infrastructure staging directory: %s", err),
		)
	}
	defer os.RemoveAll(stageDir)

	var written []ejectArtifact
	if provider == project.TerraformProviderName {
		written, err = ejectTerraform(stageDir, plan.targetPath, plan.module, plan.layer, res.Parameters)
	} else {
		written, err = ejectBicep(stageDir, plan.targetPath, plan.module, plan.layer, res.Parameters)
	}
	if err != nil {
		return err
	}

	rollback, err := installStagedInfra(stageDir, plan)
	if err != nil {
		return err
	}
	if plan.updatedYAML != nil {
		unchanged, err := azureYAMLUnchanged(yamlPath, rawYAML)
		if err != nil {
			rollback()
			return exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("re-read azure.yaml before infrastructure update: %s", err),
			)
		}
		if !unchanged {
			rollback()
			return exterrors.Validation(
				exterrors.CodeInfraEjectAzureYamlChanged,
				"azure.yaml changed while infrastructure files were being generated; eject removed its generated files",
				"review the concurrent changes and run `azd ai agent init --infra` again",
			)
		}
		if err := azdext.WriteFileAtomic(yamlPath, plan.updatedYAML, 0); err != nil {
			rollback()
			return exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("write azure.yaml after infrastructure eject: %s", err),
			)
		}
	}
	slices.SortFunc(written, func(a, b ejectArtifact) int {
		return strings.Compare(a.relPath, b.relPath)
	})
	printEjectSummary(written, plan.targetPath, plan.updateDescription)
	return nil
}

func azureYAMLUnchanged(path string, expected []byte) (bool, error) {
	current, err := os.ReadFile(path) //nolint:gosec // path is the active project's azure.yaml
	if err != nil {
		return false, err
	}
	return bytes.Equal(current, expected), nil
}

// ejectBicep writes the embedded Bicep tree plus the synthesized
// parameters file into infraDir.
func ejectBicep(
	infraDir string,
	artifactRoot string,
	module string,
	layer bool,
	params map[string]any,
) ([]ejectArtifact, error) {
	written, err := writeEmbeddedTemplates(infraDir, artifactRoot, module, layer)
	if err != nil {
		return nil, err
	}

	paramsArtifact, err := writeParametersFile(infraDir, artifactRoot, module, layer, params)
	if err != nil {
		return nil, err
	}
	written = append(written, paramsArtifact)
	return written, nil
}

// ejectTerraform writes the embedded Terraform module plus the generated
// tfvars file into infraDir.
//
// acr.tf is written only when an agent uses docker: (includeAcr). outputs.tf is
// generated to match: the ACR outputs are included only when acr.tf is present,
// and omitted entirely otherwise.
func ejectTerraform(
	infraDir string,
	artifactRoot string,
	module string,
	layer bool,
	params map[string]any,
) ([]ejectArtifact, error) {
	includeAcr, _ := params["includeAcr"].(bool)

	written, err := writeEmbeddedTerraformTemplates(infraDir, artifactRoot, includeAcr)
	if err != nil {
		return nil, err
	}
	markerArtifact, err := writeFoundryTerraformMarker(infraDir, artifactRoot)
	if err != nil {
		return nil, err
	}
	written = append(written, markerArtifact)

	outputsArtifact, err := writeOutputsFile(infraDir, artifactRoot, includeAcr, layer)
	if err != nil {
		return nil, err
	}
	written = append(written, outputsArtifact)

	tfvarsArtifact, err := writeTfvarsFile(infraDir, artifactRoot, module, layer, params)
	if err != nil {
		return nil, err
	}
	written = append(written, tfvarsArtifact)
	return written, nil
}

func writeFoundryTerraformMarker(infraDir, artifactRoot string) (ejectArtifact, error) {
	dst := filepath.Join(infraDir, foundryTerraformMarker)
	//nolint:gosec // G306: generated marker is intended to be readable by project tooling
	if err := os.WriteFile(dst, []byte(foundryTerraformV1), 0o644); err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write Terraform Foundry marker: %s", err),
		)
	}
	return ejectArtifact{
		relPath: filepath.ToSlash(filepath.Join(artifactRoot, foundryTerraformMarker)),
		bytes:   len(foundryTerraformV1),
	}, nil
}

func planInfraEject(projectRoot string, rawYAML []byte, provider string) (*infraEjectPlan, error) {
	if provider != project.BicepProviderName && provider != project.TerraformProviderName {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unsupported infrastructure provider %q", provider),
			"pass --infra=bicep or --infra=terraform",
		)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(rawYAML, &root); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"azure.yaml is not a YAML mapping at the top level",
			"verify azure.yaml is a valid azd project file",
		)
	}

	doc := root.Content[0]
	infra := mappingValue(doc, "infra")
	if infra == nil {
		infra = newMappingNode()
		doc.Content = append(doc.Content, newScalarNode("infra"), infra)
	}
	if infra.Kind != yaml.MappingNode {
		return nil, invalidInfraForEject("infra must be a mapping")
	}
	for _, key := range []string{"path", "module", "provider"} {
		if value := mappingValue(infra, key); value != nil && value.Kind != yaml.ScalarNode {
			return nil, invalidInfraForEject(fmt.Sprintf("infra.%s must be a string", key))
		}
	}

	layers := mappingValue(infra, "layers")
	if layers != nil {
		return planLayeredInfraEject(projectRoot, &root, infra, layers, provider)
	}
	return planSingleInfraEject(projectRoot, &root, infra, provider)
}

func planSingleInfraEject(
	projectRoot string,
	root *yaml.Node,
	infra *yaml.Node,
	provider string,
) (*infraEjectPlan, error) {
	existingProvider := mappingScalar(infra, "provider")
	existingName := mappingScalar(infra, "name")
	existingPath := mappingScalar(infra, "path")
	existingModule := mappingScalar(infra, "module")
	effectivePath := existingPath
	if effectivePath == "" {
		effectivePath = defaultInfraPath
	}
	effectiveModule := existingModule
	if effectiveModule == "" {
		effectiveModule = defaultInfraModule
	}
	effectiveProvider := existingProvider
	if effectiveProvider == "" {
		effectiveProvider = project.BicepProviderName
	}

	existingDir := resolveInfraPath(projectRoot, effectivePath)
	info, statErr := os.Stat(existingDir)
	dirExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat infrastructure path %s: %w", existingDir, statErr)
	}
	if dirExists && !info.IsDir() {
		return nil, ejectExistsError(filepath.ToSlash(effectivePath))
	}

	hasEntrypoint := hasInfrastructureEntrypoint(
		existingDir,
		effectiveProvider,
		effectiveModule,
	)
	customProvider := existingProvider != "" &&
		existingProvider != project.BicepProviderName &&
		existingProvider != project.TerraformProviderName &&
		existingProvider != project.FoundryProviderName
	if existingProvider == project.FoundryProviderName && hasEntrypoint {
		return nil, foundryInfraExistsError(filepath.ToSlash(effectivePath), false)
	}
	if existingProvider == project.TerraformProviderName {
		foundryTerraform, err := isFoundryTerraformInfra(existingDir, effectiveModule)
		if err != nil {
			return nil, err
		}
		if foundryTerraform {
			return nil, foundryInfraExistsError(filepath.ToSlash(effectivePath), false)
		}
	}
	if existingProvider != "" &&
		(existingProvider == project.BicepProviderName || existingProvider == project.TerraformProviderName) &&
		!hasEntrypoint {
		return nil, invalidInfraForEject(
			fmt.Sprintf("infrastructure declares provider %q but path %q contains no matching entry point",
				existingProvider, effectivePath),
		)
	}
	userOwned := existingProvider != project.FoundryProviderName && (hasEntrypoint || customProvider)
	if !userOwned {
		if dirExists && !customProvider {
			empty, err := isDirectoryEmpty(existingDir)
			if err != nil {
				return nil, err
			}
			if !empty {
				return nil, invalidInfraForEject(
					fmt.Sprintf("infrastructure path %q contains files but no detectable entry point", effectivePath),
				)
			}
		}
		changed := false
		wantedProvider := project.FoundryProviderName
		if provider == project.TerraformProviderName {
			wantedProvider = project.TerraformProviderName
		}
		if existingProvider != wantedProvider {
			setMappingScalar(infra, "provider", wantedProvider)
			changed = true
		}

		plan := &infraEjectPlan{
			targetDir:  existingDir,
			targetPath: filepath.ToSlash(effectivePath),
			module:     effectiveModule,
			layer:      false,
		}
		if changed {
			updated, err := marshalAzureYAML(root)
			if err != nil {
				return nil, err
			}
			plan.updatedYAML = updated
			plan.updateDescription = fmt.Sprintf("infra.provider: %s", wantedProvider)
		}
		return plan, nil
	}
	if sameInfraPath(effectivePath, foundryInfraLayerPath) {
		return nil, invalidInfraForEject(
			fmt.Sprintf("existing infrastructure already uses the Foundry layer path %q", foundryInfraLayerPath),
		)
	}

	existingLayer := cloneMappingNode(infra)
	if existingName == "" {
		existingName = defaultInfraPath
	}
	if existingName == foundryInfraLayerName {
		return nil, invalidInfraForEject(
			fmt.Sprintf("existing infrastructure name %q conflicts with the Foundry layer name", existingName),
		)
	}
	setMappingScalar(existingLayer, "name", existingName)
	setMappingScalar(existingLayer, "path", filepath.ToSlash(effectivePath))
	setMappingScalar(existingLayer, "provider", effectiveProvider)
	removeMappingKey(existingLayer, "layers")

	foundryLayer := newInfraLayerNode(
		foundryInfraLayerName,
		foundryInfraLayerPath,
		foundryLayerProvider(provider),
		nil,
	)
	infra.Content = nil
	infra.Content = append(infra.Content,
		newScalarNode("layers"),
		&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{existingLayer, foundryLayer}},
	)
	updated, err := marshalAzureYAML(root)
	if err != nil {
		return nil, err
	}
	return &infraEjectPlan{
		targetDir:         resolveInfraPath(projectRoot, foundryInfraLayerPath),
		targetPath:        foundryInfraLayerPath,
		module:            defaultInfraModule,
		layer:             true,
		updatedYAML:       updated,
		updateDescription: "infra.layers",
	}, nil
}

func planLayeredInfraEject(
	projectRoot string,
	root *yaml.Node,
	infra *yaml.Node,
	layers *yaml.Node,
	provider string,
) (*infraEjectPlan, error) {
	if layers.Kind != yaml.SequenceNode {
		return nil, invalidInfraForEject("infra.layers must be a sequence")
	}

	var foundryLayer *yaml.Node
	rootProvider := mappingScalar(infra, "provider")
	existingLayerNames := make([]string, 0, len(layers.Content))
	for _, layer := range layers.Content {
		if layer.Kind != yaml.MappingNode {
			return nil, invalidInfraForEject("each infra.layers entry must be a mapping")
		}
		for _, key := range []string{"name", "path", "module", "provider"} {
			if value := mappingValue(layer, key); value != nil && value.Kind != yaml.ScalarNode {
				return nil, invalidInfraForEject(
					fmt.Sprintf("infra.layers[].%s must be a string", key),
				)
			}
		}
		name := mappingScalar(layer, "name")
		if name == "" {
			return nil, invalidInfraForEject("each infra.layers entry must declare a name")
		}
		if mappingScalar(layer, "path") == "" {
			return nil, invalidInfraForEject(fmt.Sprintf("infra layer %q must declare path", name))
		}
		if slices.Contains(existingLayerNames, name) ||
			(foundryLayer != nil && mappingScalar(foundryLayer, "name") == name) {
			return nil, invalidInfraForEject(fmt.Sprintf("duplicate infrastructure layer name %q", name))
		}
		layerProvider := mappingScalar(layer, "provider")
		effectiveLayerProvider := layerProvider
		if effectiveLayerProvider == "" {
			effectiveLayerProvider = rootProvider
		}
		if effectiveLayerProvider == project.FoundryProviderName && name != foundryInfraLayerName {
			return nil, invalidInfraForEject(
				fmt.Sprintf("Foundry infrastructure already exists as layer %q; eject expects the Foundry layer name %q",
					name, foundryInfraLayerName),
			)
		}
		if name == foundryInfraLayerName &&
			(effectiveLayerProvider == project.FoundryProviderName ||
				effectiveLayerProvider == project.BicepProviderName ||
				effectiveLayerProvider == project.TerraformProviderName) {
			if foundryLayer != nil {
				return nil, invalidInfraForEject("multiple Foundry infrastructure layers are declared")
			}
			foundryLayer = layer
			continue
		}
		if name == foundryInfraLayerName {
			return nil, invalidInfraForEject(
				fmt.Sprintf("layer name %q is reserved for Foundry infrastructure", foundryInfraLayerName),
			)
		}
		existingLayerNames = append(existingLayerNames, name)
	}
	if foundryLayer != nil && len(existingLayerNames) == 0 {
		return nil, invalidInfraForEject(
			"infra.layers contains only a Foundry layer; use a root infra configuration for a Foundry-only project",
		)
	}
	if foundryLayer == nil && len(existingLayerNames) == 0 {
		return nil, invalidInfraForEject("infra.layers must contain at least one existing infrastructure layer")
	}
	if foundryLayer != nil {
		for _, layer := range layers.Content {
			if layer == foundryLayer {
				continue
			}
			if sameInfraPath(mappingScalar(layer, "path"), mappingScalar(foundryLayer, "path")) {
				return nil, invalidInfraForEject(
					fmt.Sprintf("infra layer %q already uses the Foundry layer path %q",
						mappingScalar(layer, "name"), mappingScalar(foundryLayer, "path")),
				)
			}
		}
	}
	changed := false
	if foundryLayer == nil {
		for _, layer := range layers.Content {
			if sameInfraPath(mappingScalar(layer, "path"), foundryInfraLayerPath) {
				return nil, invalidInfraForEject(
					fmt.Sprintf("infra layer %q already uses path %q",
						mappingScalar(layer, "name"), foundryInfraLayerPath),
				)
			}
		}
		foundryLayer = newInfraLayerNode(
			foundryInfraLayerName,
			foundryInfraLayerPath,
			foundryLayerProvider(provider),
			nil,
		)
		layers.Content = append(layers.Content, foundryLayer)
		changed = true
	}

	targetPath := mappingScalar(foundryLayer, "path")
	if targetPath == "" {
		return nil, invalidInfraForEject(
			fmt.Sprintf("Foundry infrastructure layer %q must declare path", mappingScalar(foundryLayer, "name")),
		)
	}
	module := mappingScalar(foundryLayer, "module")
	if module == "" {
		module = defaultInfraModule
	}
	wantedProvider := foundryLayerProvider(provider)
	currentProvider := mappingScalar(foundryLayer, "provider")
	if currentProvider == "" {
		currentProvider = rootProvider
	}
	if currentProvider == "" {
		return nil, invalidInfraForEject(
			fmt.Sprintf(
				"Foundry infrastructure layer %q must declare provider explicitly",
				mappingScalar(foundryLayer, "name"),
			),
		)
	}
	if currentProvider != "" && currentProvider != wantedProvider {
		return nil, invalidInfraForEject(
			fmt.Sprintf(
				"Foundry infrastructure layer %q already uses provider %q",
				mappingScalar(foundryLayer, "name"),
				currentProvider,
			),
		)
	}
	if mappingScalar(foundryLayer, "provider") != wantedProvider {
		setMappingScalar(foundryLayer, "provider", wantedProvider)
		changed = true
	}

	targetDir := resolveInfraPath(projectRoot, targetPath)
	info, statErr := os.Stat(targetDir)
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("stat Foundry infrastructure path %s: %w", targetDir, statErr)
	}
	if statErr == nil && !info.IsDir() {
		return nil, ejectExistsError(filepath.ToSlash(targetPath))
	}
	if hasInfrastructureEntrypoint(targetDir, wantedProvider, module) {
		return nil, foundryInfraExistsError(filepath.ToSlash(targetPath), true)
	}
	if statErr == nil {
		empty, err := isDirectoryEmpty(targetDir)
		if err != nil {
			return nil, err
		}
		if !empty {
			return nil, foundryInfraExistsError(filepath.ToSlash(targetPath), true)
		}
	}
	plan := &infraEjectPlan{
		targetDir:  targetDir,
		targetPath: filepath.ToSlash(targetPath),
		module:     module,
		layer:      true,
	}
	if changed {
		updated, err := marshalAzureYAML(root)
		if err != nil {
			return nil, err
		}
		plan.updatedYAML = updated
		plan.updateDescription = "infra.layers"
	}
	return plan, nil
}

func validateEjectTarget(projectRoot string, plan *infraEjectPlan) error {
	if plan == nil || strings.TrimSpace(plan.targetDir) == "" {
		return invalidInfraForEject("Foundry infrastructure path is empty")
	}
	if plan.module == "" || plan.module == "." || plan.module == ".." ||
		filepath.Base(plan.module) != plan.module || strings.ContainsAny(plan.module, `/\\`) {
		return invalidInfraForEject(
			fmt.Sprintf("Foundry infrastructure module %q must be a file name without path separators", plan.module),
		)
	}
	if filepath.Ext(plan.module) != "" {
		return invalidInfraForEject(
			fmt.Sprintf("Foundry infrastructure module %q must not include a file extension", plan.module),
		)
	}
	safeTarget, err := projectpaths.Join(projectRoot, plan.targetPath)
	if err != nil {
		return invalidInfraForEject(fmt.Sprintf("invalid Foundry infrastructure path %q: %s", plan.targetPath, err))
	}
	plan.targetDir = safeTarget
	if info, err := os.Stat(plan.targetDir); err == nil && !info.IsDir() {
		return ejectExistsError(plan.targetPath)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat Foundry infrastructure path %s: %w", plan.targetDir, err)
	}
	return nil
}

func installStagedInfra(stageDir string, plan *infraEjectPlan) (func(), error) {
	type stagedFile struct {
		src string
		dst string
	}
	var files []stagedFile
	err := filepath.WalkDir(stageDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stageDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(plan.targetDir, rel)
		if _, err := os.Lstat(dst); err == nil {
			return ejectExistsError(filepath.ToSlash(filepath.Join(plan.targetPath, rel)))
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat infrastructure destination %s: %w", dst, err)
		}
		files = append(files, stagedFile{src: path, dst: dst})
		return nil
	})
	if err != nil {
		return func() {}, err
	}

	createdFiles := make([]string, 0, len(files))
	createdDirs := []string{}
	rollback := func() {
		for _, path := range createdFiles {
			_ = os.Remove(path)
		}
		for i := len(createdDirs) - 1; i >= 0; i-- {
			_ = os.Remove(createdDirs[i])
		}
	}

	for _, file := range files {
		parent := filepath.Dir(file.dst)
		missing, err := missingDirectories(parent)
		if err != nil {
			rollback()
			return func() {}, err
		}
		//nolint:gosec // G301: ejected infra directories must be readable/traversable by IDEs, Git, and CI
		if err := os.MkdirAll(parent, 0o755); err != nil {
			rollback()
			return func() {}, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("create infrastructure directory %s: %s", parent, err),
			)
		}
		createdDirs = append(createdDirs, missing...)
		if err := azdext.CopyFileAtomic(file.src, file.dst, 0o644); err != nil {
			rollback()
			return func() {}, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("install infrastructure file %s: %s", file.dst, err),
			)
		}
		createdFiles = append(createdFiles, file.dst)
	}
	return rollback, nil
}

func missingDirectories(path string) ([]string, error) {
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if !info.IsDir() {
				return nil, fmt.Errorf("infrastructure parent path %s is not a directory", current)
			}
			slices.Reverse(missing)
			return missing, nil
		case errors.Is(err, fs.ErrNotExist):
			missing = append(missing, current)
		default:
			return nil, fmt.Errorf("stat infrastructure parent path %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf("no existing parent directory for infrastructure path %s", path)
		}
	}
}

func newInfraLayerNode(name, path, provider string, dependsOn []string) *yaml.Node {
	layer := &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			newScalarNode("name"), newScalarNode(name),
			newScalarNode("path"), newScalarNode(filepath.ToSlash(path)),
			newScalarNode("provider"), newScalarNode(provider),
		},
	}
	if len(dependsOn) > 0 {
		dependencies := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, dependency := range dependsOn {
			dependencies.Content = append(dependencies.Content, newScalarNode(dependency))
		}
		layer.Content = append(layer.Content, newScalarNode("dependsOn"), dependencies)
	}
	return layer
}

func foundryLayerProvider(ejectProvider string) string {
	if ejectProvider == project.TerraformProviderName {
		return project.TerraformProviderName
	}
	return project.FoundryProviderName
}

func cloneMappingNode(node *yaml.Node) *yaml.Node {
	return cloneYAMLNode(node, make(map[*yaml.Node]*yaml.Node))
}

func cloneYAMLNode(node *yaml.Node, clones map[*yaml.Node]*yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if clone, ok := clones[node]; ok {
		return clone
	}
	clone := *node
	clones[node] = &clone
	clone.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		clone.Content[i] = cloneYAMLNode(child, clones)
	}
	clone.Alias = cloneYAMLNode(node.Alias, clones)
	return &clone
}

func resolveInfraPath(projectRoot, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(projectRoot, filepath.FromSlash(path))
}

func sameInfraPath(a, b string) bool {
	left := filepath.Clean(filepath.FromSlash(a))
	right := filepath.Clean(filepath.FromSlash(b))
	return strings.EqualFold(left, right)
}

func hasInfrastructureEntrypoint(infraDir, provider, module string) bool {
	switch provider {
	case project.BicepProviderName, project.FoundryProviderName:
		return fileExists(filepath.Join(infraDir, module+".bicep")) ||
			fileExists(filepath.Join(infraDir, module+".bicepparam"))
	case project.TerraformProviderName:
		entries, err := os.ReadDir(infraDir)
		if err != nil {
			return false
		}
		return slices.ContainsFunc(entries, func(entry os.DirEntry) bool {
			return !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tf")
		})
	default:
		info, err := os.Stat(infraDir)
		return err == nil && info.IsDir()
	}
}

func isFoundryTerraformInfra(infraDir, module string) (bool, error) {
	markerPath := filepath.Join(infraDir, foundryTerraformMarker)
	markerInfo, err := os.Lstat(markerPath)
	if err == nil {
		if !markerInfo.Mode().IsRegular() {
			return false, invalidFoundryTerraformMarker(markerPath, "marker is not a regular file")
		}
		marker, err := os.ReadFile(markerPath) //nolint:gosec // validated project infra marker
		if err != nil {
			return false, invalidFoundryTerraformMarker(markerPath, err.Error())
		}
		if string(marker) != foundryTerraformV1 {
			return false, invalidFoundryTerraformMarker(markerPath, "marker version is unsupported or edited")
		}
		return true, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false, invalidFoundryTerraformMarker(markerPath, err.Error())
	}

	// Legacy ejects predate the marker. Require both the generated tfvars file
	// and distinctive Foundry resource declarations; either signal alone is
	// common in user-authored Terraform projects.
	if !fileExists(filepath.Join(infraDir, module+".tfvars.json")) {
		return false, nil
	}
	main, err := os.ReadFile(filepath.Join(infraDir, "main.tf")) //nolint:gosec // project infrastructure path
	if err != nil {
		return false, nil
	}
	source := string(main)
	return strings.Contains(source, `resource "azapi_resource" "foundry_account"`) &&
		strings.Contains(source, `resource "azapi_resource" "project"`) &&
		strings.Contains(source, "Microsoft.CognitiveServices/accounts") &&
		strings.Contains(source, "Microsoft.CognitiveServices/accounts/projects"), nil
}

func invalidFoundryTerraformMarker(path, reason string) error {
	return exterrors.Validation(
		exterrors.CodeInfraEjectMarkerInvalid,
		fmt.Sprintf("Foundry ownership marker %q cannot be used: %s; eject did not modify the infrastructure",
			filepath.ToSlash(path), reason),
		"restore the marker from source control or use an azure.ai.agents version that supports it. "+
			"If this is intentionally user-owned Terraform, remove the marker only after verifying the infrastructure",
	)
}

func isDirectoryEmpty(path string) (bool, error) {
	dir, err := os.Open(path) //nolint:gosec // path is validated as the project infrastructure directory
	if err != nil {
		return false, fmt.Errorf("open infrastructure path %s: %w", path, err)
	}
	defer dir.Close()
	_, err = dir.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read infrastructure path %s: %w", path, err)
	}
	return false, nil
}

func marshalAzureYAML(root *yaml.Node) ([]byte, error) {
	out, err := yaml.Marshal(root)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("marshal azure.yaml after infrastructure eject: %s", err),
		)
	}
	return out, nil
}

func invalidInfraForEject(message string) error {
	return exterrors.Validation(
		exterrors.CodeInvalidAzureYaml,
		message,
		"fix the infra configuration in azure.yaml and run the command again",
	)
}

func ejectExistsError(path string) error {
	return exterrors.Validation(
		exterrors.CodeInfraEjectExists,
		fmt.Sprintf("`./%s` already exists", filepath.ToSlash(path)),
		"remove the conflicting path or edit the existing infrastructure manually. To use another Foundry path, "+
			"first declare an infra.layers entry named 'foundry' with a different project-relative path in azure.yaml",
	)
}

func foundryInfraExistsError(path string, layer bool) error {
	location := "Foundry infrastructure"
	suggestion := "remove the existing generated Foundry infrastructure before ejecting it again, or edit it manually"
	if layer {
		location = fmt.Sprintf("Foundry infrastructure layer %q", foundryInfraLayerName)
		suggestion += ". To use another path, change the project-relative path on the 'foundry' entry in infra.layers"
	}
	return exterrors.Validation(
		exterrors.CodeInfraEjectExists,
		fmt.Sprintf("%s already exists at `./%s`; eject did not overwrite it", location, filepath.ToSlash(path)),
		suggestion,
	)
}

func newMappingNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func newScalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func mappingScalar(mapping *yaml.Node, key string) string {
	value := mappingValue(mapping, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(value.Value)
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
func writeEmbeddedTemplates(
	infraDir string,
	artifactRoot string,
	module string,
	layer bool,
) (_ []ejectArtifact, retErr error) {
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
		dstRel := rel
		if rel == "main.bicep" {
			dstRel = module + ".bicep"
		}
		dst := filepath.Join(infraDir, filepath.FromSlash(dstRel))

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
			relPath: filepath.ToSlash(filepath.Join(artifactRoot, dstRel)),
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
func writeParametersFile(
	infraDir string,
	artifactRoot string,
	module string,
	layer bool,
	params map[string]any,
) (ejectArtifact, error) {
	type paramValue struct {
		Value any `json:"value"`
	}
	wrapped := map[string]paramValue{}
	for k, v := range params {
		wrapped[k] = paramValue{Value: v}
	}
	if layer {
		wrapped["resourceGroupName"] = paramValue{
			Value: "${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}",
		}
		wrapped["location"] = paramValue{Value: "${AZURE_LOCATION}"}
		wrapped["foundryProjectName"] = paramValue{Value: "${AZURE_AI_PROJECT_NAME=${AZURE_ENV_NAME}}"}
		wrapped["resourceTokenSalt"] = paramValue{Value: "${AZURE_RESOURCE_TOKEN_SALT}"}
		wrapped["principalId"] = paramValue{Value: "${AZURE_PRINCIPAL_ID}"}
		wrapped["principalType"] = paramValue{Value: "${AZURE_PRINCIPAL_TYPE}"}
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

	filename := module + ".parameters.json"
	dst := filepath.Join(infraDir, filename)
	//nolint:gosec // G306: ejected parameters file is intended to be human-readable
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write main.parameters.json: %s", err),
		)
	}
	return ejectArtifact{
		relPath: filepath.ToSlash(filepath.Join(artifactRoot, filename)),
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
func writeEmbeddedTerraformTemplates(
	infraDir string,
	artifactRoot string,
	includeAcr bool,
) (_ []ejectArtifact, retErr error) {
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
			relPath: filepath.ToSlash(filepath.Join(artifactRoot, name)),
			bytes:   len(data),
		})
	}

	return artifacts, nil
}

// writeOutputsFile renders infra/outputs.tf from the embedded outputs.tf.tmpl.
// The ACR outputs are included only when includeAcr is true (acr.tf was
// written); otherwise they are omitted entirely, since Terraform resolves
// resource references statically and acr.tf's resources are not present.
func writeOutputsFile(
	infraDir string,
	artifactRoot string,
	includeAcr bool,
	layer bool,
) (ejectArtifact, error) {
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
	if layer {
		const resourceGroupOutput = "output \"AZURE_RESOURCE_GROUP\" {"
		contents := buf.String()
		start := strings.Index(contents, resourceGroupOutput)
		if start < 0 || strings.Index(contents[start+len(resourceGroupOutput):], resourceGroupOutput) >= 0 {
			return ejectArtifact{}, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				"render outputs.tf: expected exactly one AZURE_RESOURCE_GROUP output",
			)
		}
		end := strings.Index(contents[start:], "}")
		if end < 0 {
			return ejectArtifact{}, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				"render outputs.tf: malformed AZURE_RESOURCE_GROUP output",
			)
		}
		end += start + len("}")
		for end < len(contents) && (contents[end] == '\r' || contents[end] == '\n') {
			end++
		}
		buf = *bytes.NewBufferString(contents[:start] + contents[end:])
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
		relPath: filepath.ToSlash(filepath.Join(artifactRoot, "outputs.tf")),
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
func writeTfvarsFile(
	infraDir string,
	artifactRoot string,
	module string,
	layer bool,
	params map[string]any,
) (ejectArtifact, error) {
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
	if layer {
		doc["resource_group_name"] = "${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}"
		doc["foundry_project_name"] = "${AZURE_AI_PROJECT_NAME=${AZURE_ENV_NAME}}"
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

	filename := module + ".tfvars.json"
	dst := filepath.Join(infraDir, filename)
	//nolint:gosec // G306: ejected tfvars file is intended to be human-readable
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write main.tfvars.json: %s", err),
		)
	}
	return ejectArtifact{
		relPath: filepath.ToSlash(filepath.Join(artifactRoot, filename)),
		bytes:   len(data),
	}, nil
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

// printEjectSummary renders the user-facing success block to stdout and notes
// any azure.yaml provider/layer update.
func printEjectSummary(written []ejectArtifact, targetPath string, updateDescription string) {
	fmt.Println()
	fmt.Println("Generating infrastructure files from azure.yaml...")
	fmt.Println()
	for _, a := range written {
		fmt.Printf("  %s %s\n", color.GreenString("Created"), a.relPath)
	}
	fmt.Println()
	if updateDescription != "" {
		fmt.Printf("  %s azure.yaml (%s)\n", color.GreenString("Updated"), updateDescription)
		fmt.Println()
	}
	fmt.Printf("Future provisions will read the Foundry layer from ./%s/.\n", filepath.ToSlash(targetPath))
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  azd provision    Apply changes")
	fmt.Println()
}
