// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
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
	projectPaths "azureaiagent/internal/pkg/paths"
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
	mergeExisting     bool
	updatedYAML       []byte
	updateDescription string
}

type infraTargetState struct {
	dir    string
	exists bool
	empty  bool
}

type infraConfig struct {
	root         *yaml.Node
	infra        *yaml.Node
	layersNode   *yaml.Node
	rootProvider string
	layers       []infraLayer
}

type infraLayer struct {
	node              *yaml.Node
	name              string
	path              string
	module            string
	provider          string
	effectiveProvider string
}

type infraEjectAcrMode string

const (
	infraEjectAcrNone             infraEjectAcrMode = "none"
	infraEjectAcrCreate           infraEjectAcrMode = "create"
	infraEjectAcrReuseConnect     infraEjectAcrMode = "reuse-connect"
	infraEjectAcrAlreadyConnected infraEjectAcrMode = "already-connected"
)

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
// Infrastructure compatibility is handled by the layer-aware eject planner.
// The gate only decides whether init must add a Foundry service first.
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

	if hasFoundry {
		return infraGate{standaloneEject: true, projectRoot: projectRoot}, nil
	}
	return infraGate{projectRoot: projectRoot}, nil
}

// ejectInfraAfterInit ejects from the azd project containing the current
// directory. Init may create or discover a project above cwd, so use the same
// upward project resolution as the rest of azd.
func ejectInfraAfterInit(ctx context.Context, provider string, clients ...*azdext.AzdClient) error {
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

	var env map[string]string
	needsEnv, err := infraEjectNeedsEnvironment(projectRoot)
	if err != nil {
		return err
	}
	if needsEnv && len(clients) > 0 && clients[0] != nil {
		env, err = readInfraEjectEnvironment(ctx, clients[0])
		if err != nil {
			return err
		}
	}
	return ejectInfra(projectRoot, provider, env)
}

func infraEjectNeedsEnvironment(projectRoot string) (bool, error) {
	rawYAML, err := readProjectAzureYAML(projectRoot)
	if err != nil {
		return false, err
	}
	serviceName, err := findFoundryServiceForEject(rawYAML)
	if err != nil {
		return false, err
	}
	endpoint, err := synthesis.ProjectEndpoint(rawYAML, serviceName, projectRoot)
	if err != nil {
		return false, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("read endpoint for foundry project service %q: %s", serviceName, err),
			"check the endpoint field under your azure.ai.project service",
		)
	}
	return endpoint != "", nil
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
func ejectInfra(projectRoot, provider string, environments ...map[string]string) error {
	yamlPath := filepath.Join(projectRoot, "azure.yaml")
	rawYAML, err := readProjectAzureYAML(projectRoot)
	if err != nil {
		return err
	}

	svcName, err := findFoundryServiceForEject(rawYAML)
	if err != nil {
		return err
	}
	endpoint, err := synthesis.ProjectEndpoint(rawYAML, svcName, projectRoot)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("read endpoint for foundry project service %q: %s", svcName, err),
			"check the endpoint field under your azure.ai.project service",
		)
	}
	existingProject := endpoint != ""
	if existingProject && len(environments) > 0 {
		if err := validateExistingProjectEjectEnvironment(endpoint, environments[0]); err != nil {
			return err
		}
	}

	layerProvider := foundryLayerProvider(provider)
	plan, err := planInfraEject(projectRoot, rawYAML, provider, layerProvider)
	if err != nil {
		return err
	}
	if err := validateEjectTarget(plan); err != nil {
		return err
	}

	synthesisInput := synthesis.Input{
		RawAzureYAML:  rawYAML,
		ServiceName:   svcName,
		AcceptedHosts: project.FoundryProvisioningServiceHosts,
		ProjectRoot:   projectRoot,
		// Eject writes a static infra/ tree. Keep ${VAR} references verbatim so
		// the ejected main.parameters.json stays environment-portable; the
		// on-disk provision flow resolves them from the azd environment.
		PreserveVarRefs: true,
	}
	var res *synthesis.Result
	if existingProject {
		res, err = synthesis.SynthesizeExistingProject(synthesisInput)
	} else {
		res, err = synthesis.Synthesize(synthesisInput)
	}
	if err != nil {
		// Reuse the provider's vocabulary so eject and provision report
		// consistent codes for the same azure.yaml problems.
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("synthesize foundry project service %q: %s", svcName, err),
			"check the endpoint, deployments, and network fields under your azure.ai.project service",
		)
	}
	acrMode := infraEjectAcrNone
	if existingProject {
		var values map[string]string
		if len(environments) > 0 {
			values = environments[0]
		}
		acrMode, err = resolveInfraEjectAcrMode(res.Parameters, values)
		if err != nil {
			return err
		}
		if provider == project.TerraformProviderName && acrMode == infraEjectAcrCreate &&
			(strings.TrimSpace(values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]) != "" ||
				strings.TrimSpace(values["AZURE_CONTAINER_REGISTRY_ENDPOINT"]) != "" ||
				strings.TrimSpace(values["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"]) != "" ||
				strings.TrimSpace(values["AZD_FOUNDRY_RESOURCE_GROUP_ID"]) != "") {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"Terraform eject cannot adopt the container registry previously created by microsoft.foundry",
				"run `azd down` before ejecting Terraform, or keep Bicep/microsoft.foundry for the existing resources",
			)
		}
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
	if existingProject && provider == project.TerraformProviderName {
		written, err = ejectExistingProjectTerraform(
			stageDir, plan.targetPath, plan.module, res.Parameters, acrMode, environments)
	} else if provider == project.TerraformProviderName {
		written, err = ejectTerraform(stageDir, plan.targetPath, plan.module, plan.layer, res.Parameters)
	} else if existingProject {
		written, err = ejectExistingProjectBicep(
			stageDir, plan.targetPath, plan.module, res.Parameters, acrMode, environments)
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

func ejectExistingProjectBicep(
	infraDir string,
	artifactRoot string,
	module string,
	params map[string]any,
	acrMode infraEjectAcrMode,
	environments []map[string]string,
) ([]ejectArtifact, error) {
	acrPullAssigned := len(environments) > 0 && strings.EqualFold(
		strings.TrimSpace(environments[0]["AZD_FOUNDRY_ACR_PULL_ASSIGNED"]), "true")
	written, err := writeExistingProjectBicepTemplates(
		infraDir, artifactRoot, module, acrMode, acrPullAssigned)
	if err != nil {
		return nil, err
	}
	params["projectResourceId"] = "${AZURE_AI_PROJECT_ID}"
	params["projectEndpoint"] = "${FOUNDRY_PROJECT_ENDPOINT}"
	delete(params, "includeAcr")
	if acrMode == infraEjectAcrCreate {
		params["resourceGroupName"] = "${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}"
		params["location"] = "${AZURE_LOCATION}"
		params["resourceTokenSalt"] = "${AZD_RESOURCE_TOKEN_SALT}"
		params["tags"] = map[string]string{"azd-env-name": "${AZURE_ENV_NAME}"}
	} else if (acrMode == infraEjectAcrReuseConnect || acrMode == infraEjectAcrAlreadyConnected) &&
		len(environments) > 0 {
		params["existingAcrEndpoint"] = environments[0]["AZURE_CONTAINER_REGISTRY_ENDPOINT"]
		params["existingAcrResourceId"] = environments[0]["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
		if acrMode == infraEjectAcrAlreadyConnected {
			params["existingAcrConnectionName"] = environments[0]["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"]
		}
	}
	paramsArtifact, err := writeParametersFile(infraDir, artifactRoot, module, false, params)
	if err != nil {
		return nil, err
	}
	return append(written, paramsArtifact), nil
}

// ejectTerraform writes the embedded Terraform module plus the generated
// tfvars file into infraDir.
//
// container-registry.tf is written only when an agent uses docker: (includeAcr). outputs.tf is
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

func ejectExistingProjectTerraform(
	infraDir string,
	artifactRoot string,
	module string,
	params map[string]any,
	acrMode infraEjectAcrMode,
	environments []map[string]string,
) ([]ejectArtifact, error) {
	acrPullAssigned := len(environments) > 0 && strings.EqualFold(
		strings.TrimSpace(environments[0]["AZD_FOUNDRY_ACR_PULL_ASSIGNED"]), "true")
	written, err := writeExistingProjectTerraformTemplates(
		infraDir, artifactRoot, acrMode, acrPullAssigned)
	if err != nil {
		return nil, err
	}
	markerArtifact, err := writeFoundryTerraformMarker(infraDir, artifactRoot)
	if err != nil {
		return nil, err
	}
	written = append(written, markerArtifact)
	outputsArtifact, err := writeTerraformOutputsFile(
		infraDir,
		artifactRoot,
		acrMode != infraEjectAcrNone,
		"templates/terraform-existing-project/outputs.tf.tmpl",
		synthesis.ExistingProjectTerraformTemplatesFS(),
		false,
		string(acrMode),
	)
	if err != nil {
		return nil, err
	}
	written = append(written, outputsArtifact)
	tfvarsArtifact, err := writeExistingProjectTfvarsFile(
		infraDir, artifactRoot, module, params, environments)
	if err != nil {
		return nil, err
	}
	return append(written, tfvarsArtifact), nil
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

func planInfraEject(
	projectRoot string,
	rawYAML []byte,
	provider string,
	layerProvider string,
) (*infraEjectPlan, error) {
	if provider != project.BicepProviderName && provider != project.TerraformProviderName {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unsupported infrastructure provider %q", provider),
			"pass --infra=bicep or --infra=terraform",
		)
	}
	config, err := parseInfraConfig(rawYAML)
	if err != nil {
		return nil, err
	}
	if config.layersNode == nil {
		return planRootInfraEject(projectRoot, config, layerProvider)
	}
	return planLayeredInfraEject(projectRoot, config, layerProvider)
}

func parseInfraConfig(rawYAML []byte) (*infraConfig, error) {
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
	for _, key := range []string{"name", "path", "module", "provider"} {
		if value := mappingValue(infra, key); value != nil && value.Kind != yaml.ScalarNode {
			return nil, invalidInfraForEject(fmt.Sprintf("infra.%s must be a string", key))
		}
	}
	config := &infraConfig{
		root:         &root,
		infra:        infra,
		layersNode:   mappingValue(infra, "layers"),
		rootProvider: mappingScalar(infra, "provider"),
	}
	if config.layersNode == nil {
		return config, nil
	}
	if config.layersNode.Kind != yaml.SequenceNode {
		return nil, invalidInfraForEject("infra.layers must be a sequence")
	}
	seen := make(map[string]struct{}, len(config.layersNode.Content))
	for _, node := range config.layersNode.Content {
		if node.Kind != yaml.MappingNode {
			return nil, invalidInfraForEject("each infra.layers entry must be a mapping")
		}
		for _, key := range []string{"name", "path", "module", "provider"} {
			if value := mappingValue(node, key); value != nil && value.Kind != yaml.ScalarNode {
				return nil, invalidInfraForEject(fmt.Sprintf("infra.layers[].%s must be a string", key))
			}
		}
		layer := infraLayer{
			node:     node,
			name:     mappingScalar(node, "name"),
			path:     mappingScalar(node, "path"),
			module:   mappingScalar(node, "module"),
			provider: mappingScalar(node, "provider"),
		}
		name := layer.name
		if name == "" {
			return nil, invalidInfraForEject("each infra.layers entry must declare a name")
		}
		if layer.path == "" {
			return nil, invalidInfraForEject(fmt.Sprintf("infra layer %q must declare path", name))
		}
		if _, ok := seen[name]; ok {
			return nil, invalidInfraForEject(fmt.Sprintf("duplicate infrastructure layer name %q", name))
		}
		seen[name] = struct{}{}
		layer.effectiveProvider = layer.provider
		if layer.effectiveProvider == "" {
			layer.effectiveProvider = config.rootProvider
		}
		config.layers = append(config.layers, layer)
	}
	return config, nil
}

func planRootInfraEject(
	projectRoot string,
	config *infraConfig,
	layerProvider string,
) (*infraEjectPlan, error) {
	root := infraLayer{
		node:              config.infra,
		name:              valueOrDefault(mappingScalar(config.infra, "name"), defaultInfraPath),
		path:              valueOrDefault(mappingScalar(config.infra, "path"), defaultInfraPath),
		module:            valueOrDefault(mappingScalar(config.infra, "module"), defaultInfraModule),
		provider:          config.rootProvider,
		effectiveProvider: valueOrDefault(config.rootProvider, project.BicepProviderName),
	}
	target, err := inspectInfraTarget(projectRoot, root.path)
	if err != nil {
		return nil, err
	}
	userOwned, err := rootInfraUserOwned(root, target)
	if err != nil {
		return nil, err
	}
	if !userOwned {
		wanted := layerProvider
		changed := root.provider != wanted
		if changed {
			setMappingScalar(config.infra, "provider", wanted)
		}
		return newInfraEjectPlan(config, target, root.path, root.module, false, changed,
			fmt.Sprintf("infra.provider: %s", wanted))
	}
	if sameInfraPath(root.path, foundryInfraLayerPath) {
		return nil, invalidInfraForEject(
			fmt.Sprintf("existing infrastructure already uses the Foundry layer path %q", foundryInfraLayerPath),
		)
	}
	if root.name == foundryInfraLayerName {
		return nil, invalidInfraForEject(
			fmt.Sprintf("existing infrastructure name %q conflicts with the Foundry layer name", root.name),
		)
	}
	existingLayerValue := *config.infra
	existingLayer := &existingLayerValue
	setMappingScalar(existingLayer, "name", root.name)
	setMappingScalar(existingLayer, "path", filepath.ToSlash(root.path))
	setMappingScalar(existingLayer, "provider", root.effectiveProvider)
	removeMappingKey(existingLayer, "layers")
	foundryLayer := newInfraLayerNode(foundryInfraLayerName, foundryInfraLayerPath, layerProvider)
	config.infra.Content = []*yaml.Node{
		newScalarNode("layers"),
		{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{existingLayer, foundryLayer}},
	}
	foundryTarget, err := inspectInfraTarget(projectRoot, foundryInfraLayerPath)
	if err != nil {
		return nil, err
	}
	plan, err := newInfraEjectPlan(
		config, foundryTarget, foundryInfraLayerPath, defaultInfraModule, true, true, "infra.layers")
	if plan != nil {
		plan.mergeExisting = foundryTarget.exists
	}
	return plan, err
}

func planLayeredInfraEject(
	projectRoot string,
	config *infraConfig,
	layerProvider string,
) (*infraEjectPlan, error) {
	var foundry *infraLayer
	for i := range config.layers {
		layer := &config.layers[i]
		if layer.effectiveProvider == project.FoundryProviderName && layer.name != foundryInfraLayerName {
			return nil, invalidInfraForEject(
				fmt.Sprintf("Foundry infrastructure already exists as layer %q; eject expects the Foundry layer name %q",
					layer.name, foundryInfraLayerName),
			)
		}
		if layer.name != foundryInfraLayerName {
			continue
		}
		if !isFoundryLayerProvider(layer.effectiveProvider) {
			return nil, invalidInfraForEject(
				fmt.Sprintf("layer name %q is reserved for Foundry infrastructure", foundryInfraLayerName),
			)
		}
		foundry = layer
	}
	if foundry != nil && len(config.layers) == 1 {
		return nil, invalidInfraForEject(
			"infra.layers contains only a Foundry layer; use a root infra configuration for a Foundry-only project",
		)
	}
	if len(config.layers) == 0 {
		return nil, invalidInfraForEject("infra.layers must contain at least one existing infrastructure layer")
	}
	changed := false
	newLayer := foundry == nil
	if foundry == nil {
		node := newInfraLayerNode(foundryInfraLayerName, foundryInfraLayerPath, layerProvider)
		config.layersNode.Content = append(config.layersNode.Content, node)
		config.layers = append(config.layers, infraLayer{
			node: node, name: foundryInfraLayerName, path: foundryInfraLayerPath,
			provider: layerProvider, effectiveProvider: layerProvider,
		})
		foundry = &config.layers[len(config.layers)-1]
		changed = true
	}
	wantedProvider := layerProvider
	if foundry.effectiveProvider == "" {
		return nil, invalidInfraForEject(
			fmt.Sprintf("Foundry infrastructure layer %q must declare provider explicitly", foundry.name),
		)
	}
	if foundry.effectiveProvider != wantedProvider && foundry.effectiveProvider != project.FoundryProviderName {
		return nil, invalidInfraForEject(
			fmt.Sprintf("Foundry infrastructure layer %q already uses provider %q", foundry.name, foundry.effectiveProvider),
		)
	}
	if foundry.provider != wantedProvider {
		setMappingScalar(foundry.node, "provider", wantedProvider)
		changed = true
	}
	for i := range config.layers {
		layer := &config.layers[i]
		if layer != foundry && sameInfraPath(layer.path, foundry.path) {
			return nil, invalidInfraForEject(
				fmt.Sprintf("infra layer %q already uses the Foundry layer path %q", layer.name, foundry.path),
			)
		}
	}
	target, err := inspectInfraTarget(projectRoot, foundry.path)
	if err != nil {
		return nil, err
	}
	if !newLayer && (target.exists && !target.empty ||
		hasInfrastructureEntrypoint(target.dir, wantedProvider, valueOrDefault(foundry.module, defaultInfraModule))) {
		return nil, foundryInfraExistsError(filepath.ToSlash(foundry.path), true)
	}
	plan, err := newInfraEjectPlan(config, target, foundry.path, valueOrDefault(foundry.module, defaultInfraModule),
		true, changed, "infra.layers")
	if plan != nil {
		plan.mergeExisting = newLayer && target.exists
	}
	return plan, err
}

func writeExistingProjectBicepTemplates(
	infraDir string,
	artifactRoot string,
	module string,
	acrMode infraEjectAcrMode,
	acrPullAssigned bool,
) ([]ejectArtifact, error) {
	if acrMode != infraEjectAcrNone && acrMode != infraEjectAcrCreate &&
		acrMode != infraEjectAcrReuseConnect && acrMode != infraEjectAcrAlreadyConnected {
		return nil, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("unsupported existing-project ACR mode %q", acrMode),
		)
	}
	//nolint:gosec // generated infrastructure must be readable by project tooling
	if err := os.MkdirAll(infraDir, 0o755); err != nil {
		return nil, infraInstallError("create infrastructure directory", err)
	}
	entrypoint, err := fs.ReadFile(synthesis.TemplatesFS(), "templates/existing-project-eject.bicep.tmpl")
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("read existing-project Bicep template: %s", err),
		)
	}
	tmpl, err := template.New("existing-project.bicep").Parse(string(entrypoint))
	if err != nil {
		return nil, exterrors.Internal(exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("parse existing-project Bicep template: %s", err))
	}
	var rendered bytes.Buffer
	renderData := struct {
		AcrMode         string
		AcrPullAssigned bool
	}{AcrMode: string(acrMode), AcrPullAssigned: acrPullAssigned}
	if err := tmpl.Execute(&rendered, renderData); err != nil {
		return nil, exterrors.Internal(exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("render existing-project Bicep template: %s", err))
	}
	entrypointPath := filepath.Join(infraDir, module+".bicep")
	//nolint:gosec // G306: ejected Bicep sources are intended to be human-readable
	if err := os.WriteFile(entrypointPath, rendered.Bytes(), 0o644); err != nil {
		return nil, infraInstallError("write existing-project Bicep entrypoint", err)
	}
	artifacts := []ejectArtifact{{
		relPath: filepath.ToSlash(filepath.Join(artifactRoot, module+".bicep")),
		bytes:   rendered.Len(),
	}}

	files := []struct {
		source string
		target string
	}{
		{"templates/modules/foundry-project.bicep", "modules/foundry-project.bicep"},
	}
	if acrMode == infraEjectAcrCreate || (acrMode == infraEjectAcrReuseConnect && !acrPullAssigned) {
		registrySource, err := fs.ReadFile(
			synthesis.TemplatesFS(), "templates/modules/container-registry-eject.bicep.tmpl")
		if err != nil {
			return nil, exterrors.Internal(exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("read container registry Bicep template: %s", err))
		}
		registryTemplate, err := template.New("container-registry.bicep").Parse(string(registrySource))
		if err != nil {
			return nil, exterrors.Internal(exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("parse container registry Bicep template: %s", err))
		}
		var registry bytes.Buffer
		if err := registryTemplate.Execute(&registry, renderData); err != nil {
			return nil, exterrors.Internal(exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("render container registry Bicep template: %s", err))
		}
		registryPath := filepath.Join(infraDir, "modules", "container-registry.bicep")
		//nolint:gosec // G301: ejected infra directories must be readable/traversable by IDEs, Git, and CI
		if err := os.MkdirAll(filepath.Dir(registryPath), 0o755); err != nil {
			return nil, infraInstallError("create existing-project Bicep module directory", err)
		}
		//nolint:gosec // G306: ejected Bicep sources are intended to be human-readable
		if err := os.WriteFile(registryPath, registry.Bytes(), 0o644); err != nil {
			return nil, infraInstallError("write container registry Bicep module", err)
		}
		artifacts = append(artifacts, ejectArtifact{
			relPath: filepath.ToSlash(filepath.Join(artifactRoot, "modules/container-registry.bicep")),
			bytes:   registry.Len(),
		})
	}
	for _, file := range files {
		data, err := fs.ReadFile(synthesis.TemplatesFS(), file.source)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("read existing-project Bicep template %s: %s", file.source, err),
			)
		}
		destination := filepath.Join(infraDir, filepath.FromSlash(file.target))
		//nolint:gosec // generated infrastructure directories must be readable by project tooling
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return nil, infraInstallError("create existing-project Bicep module directory", err)
		}
		//nolint:gosec // generated Bicep is intended to be human-readable
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("write existing-project Bicep template %s: %s", file.target, err),
			)
		}
		artifacts = append(artifacts, ejectArtifact{
			relPath: filepath.ToSlash(filepath.Join(artifactRoot, file.target)),
			bytes:   len(data),
		})
	}
	return artifacts, nil
}

func rootInfraUserOwned(layer infraLayer, target infraTargetState) (bool, error) {
	hasEntrypoint := hasInfrastructureEntrypoint(target.dir, layer.effectiveProvider, layer.module)
	if layer.provider == project.FoundryProviderName && hasEntrypoint {
		return false, foundryInfraExistsError(filepath.ToSlash(layer.path), false)
	}
	if layer.provider == project.TerraformProviderName {
		foundry, err := isFoundryTerraformInfra(target.dir, layer.module)
		if err != nil {
			return false, err
		}
		if foundry {
			return false, foundryInfraExistsError(filepath.ToSlash(layer.path), false)
		}
	}
	builtIn := layer.provider == project.BicepProviderName || layer.provider == project.TerraformProviderName
	if layer.provider != "" && builtIn && !hasEntrypoint {
		return false, invalidInfraForEject(fmt.Sprintf(
			"infrastructure declares provider %q but path %q contains no matching entry point", layer.provider, layer.path))
	}
	custom := layer.provider != "" && !builtIn && layer.provider != project.FoundryProviderName
	if !hasEntrypoint && !custom && target.exists && !target.empty {
		return false, invalidInfraForEject(
			fmt.Sprintf("infrastructure path %q contains files but no detectable entry point", layer.path))
	}
	return layer.provider != project.FoundryProviderName && (hasEntrypoint || custom), nil
}

func newInfraEjectPlan(
	config *infraConfig,
	target infraTargetState,
	path, module string,
	layer, changed bool,
	description string,
) (*infraEjectPlan, error) {
	plan := &infraEjectPlan{
		targetDir: target.dir, targetPath: filepath.ToSlash(path), module: module, layer: layer,
	}
	if changed {
		updated, err := marshalAzureYAML(config.root)
		if err != nil {
			return nil, err
		}
		plan.updatedYAML, plan.updateDescription = updated, description
	}
	return plan, nil
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func isFoundryLayerProvider(provider string) bool {
	return provider == project.FoundryProviderName ||
		provider == project.BicepProviderName ||
		provider == project.TerraformProviderName
}

func validateEjectTarget(plan *infraEjectPlan) error {
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
	return nil
}

func installStagedInfra(stageDir string, plan *infraEjectPlan) (func(), error) {
	if plan.mergeExisting {
		return mergeStagedInfra(stageDir, plan)
	}
	parent := filepath.Dir(plan.targetDir)
	existingParent, err := existingDirectory(parent)
	if err != nil {
		return func() {}, err
	}
	//nolint:gosec // G301: ejected infra directories must be readable/traversable by IDEs, Git, and CI
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return func() {}, infraInstallError("create infrastructure parent directory", err)
	}

	restoreEmptyTarget := false
	if info, err := os.Lstat(plan.targetDir); err == nil {
		if !info.IsDir() {
			return func() {}, ejectExistsError(plan.targetPath)
		}
		empty, err := dirIsEmpty(plan.targetDir)
		if err != nil {
			return func() {}, fmt.Errorf("inspect infrastructure destination %s: %w", plan.targetDir, err)
		}
		if !empty {
			return func() {}, foundryInfraExistsError(plan.targetPath, plan.layer)
		}
		if err := os.Remove(plan.targetDir); err != nil {
			return func() {}, infraInstallError("prepare empty infrastructure destination", err)
		}
		restoreEmptyTarget = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return func() {}, fmt.Errorf("stat infrastructure destination %s: %w", plan.targetDir, err)
	}

	if err := os.Rename(stageDir, plan.targetDir); err != nil {
		if restoreEmptyTarget {
			//nolint:gosec // G301: restore the readable/traversable empty project directory removed above
			_ = os.Mkdir(plan.targetDir, 0o755)
		}
		removeEmptyParents(parent, existingParent)
		return func() {}, infraInstallError("install infrastructure directory", err)
	}
	return func() {
		_ = os.RemoveAll(plan.targetDir)
		if restoreEmptyTarget {
			//nolint:gosec // G301: restore the readable/traversable empty project directory removed above
			_ = os.Mkdir(plan.targetDir, 0o755)
		}
		removeEmptyParents(parent, existingParent)
	}, nil
}

func mergeStagedInfra(stageDir string, plan *infraEjectPlan) (func(), error) {
	type stagedFile struct {
		src string
		dst string
	}
	var files []stagedFile
	err := filepath.WalkDir(stageDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(stageDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(plan.targetDir, rel)
		if err := rejectSymlinkedParents(plan.targetDir, filepath.Dir(dst)); err != nil {
			return err
		}
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
	rollback := func() {
		for i := len(createdFiles) - 1; i >= 0; i-- {
			_ = os.Remove(createdFiles[i])
			removeEmptyParents(filepath.Dir(createdFiles[i]), plan.targetDir)
		}
	}
	for _, file := range files {
		//nolint:gosec // G301: generated infrastructure directories must be readable by project tooling
		if err := os.MkdirAll(filepath.Dir(file.dst), 0o755); err != nil {
			rollback()
			return func() {}, infraInstallError("create infrastructure directory", err)
		}
		if err := azdext.CopyFileAtomic(file.src, file.dst, 0o644); err != nil {
			rollback()
			return func() {}, infraInstallError("install infrastructure file", err)
		}
		createdFiles = append(createdFiles, file.dst)
	}
	return rollback, nil
}

func rejectSymlinkedParents(root, parent string) error {
	rootInfo, err := os.Lstat(root)
	if err == nil && rootInfo.Mode()&os.ModeSymlink != 0 {
		return invalidInfraForEject(fmt.Sprintf("infrastructure destination root %q is a symbolic link", root))
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect infrastructure destination root %s: %w", root, err)
	}
	rel, err := filepath.Rel(root, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return invalidInfraForEject(fmt.Sprintf("infrastructure destination %q escapes its target directory", parent))
	}
	current := root
	for _, component := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect infrastructure destination parent %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return invalidInfraForEject(
				fmt.Sprintf("infrastructure destination parent %q is a symbolic link", current),
			)
		}
		if !info.IsDir() {
			return invalidInfraForEject(fmt.Sprintf("infrastructure destination parent %q is not a directory", current))
		}
	}
	return nil
}

func infraInstallError(action string, err error) error {
	return exterrors.Internal(exterrors.CodeInfraEjectWriteFailed, fmt.Sprintf("%s: %s", action, err))
}

func existingDirectory(path string) (string, error) {
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() {
				return "", fmt.Errorf("infrastructure parent path %s is not a directory", current)
			}
			return current, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("stat infrastructure parent path %s: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing parent directory for infrastructure path %s", path)
		}
	}
}

func removeEmptyParents(path, stop string) {
	for path != stop {
		_ = os.Remove(path)
		path = filepath.Dir(path)
	}
}

func newInfraLayerNode(name, path, provider string) *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			newScalarNode("name"), newScalarNode(name),
			newScalarNode("path"), newScalarNode(filepath.ToSlash(path)),
			newScalarNode("provider"), newScalarNode(provider),
		},
	}
}

func foundryLayerProvider(ejectProvider string) string {
	if ejectProvider == project.TerraformProviderName {
		return project.TerraformProviderName
	}
	return project.FoundryProviderName
}

func inspectInfraTarget(projectRoot, path string) (infraTargetState, error) {
	dir, err := projectPaths.Join(projectRoot, path)
	if err != nil {
		return infraTargetState{}, invalidInfraForEject(
			fmt.Sprintf("invalid Foundry infrastructure path %q: %s", path, err),
		)
	}
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return infraTargetState{dir: dir}, nil
	}
	if err != nil {
		return infraTargetState{}, fmt.Errorf("stat infrastructure path %s: %w", dir, err)
	}
	if !info.IsDir() {
		return infraTargetState{}, ejectExistsError(filepath.ToSlash(path))
	}
	empty, err := dirIsEmpty(dir)
	if err != nil {
		return infraTargetState{}, fmt.Errorf("inspect infrastructure path %s: %w", dir, err)
	}
	return infraTargetState{dir: dir, exists: true, empty: empty}, nil
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
// Provider-runtime and existing-project files are skipped:
//   - main.arm.json (the pre-compiled ARM JSON): would be stale once the user
//     edits main.bicep.
//   - existing-project.bicep and its modules: emitted only by the dedicated
//     existing-project writer.
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

		base := filepath.Base(p)
		if base == "existing-project-eject.bicep.tmpl" {
			return nil
		}
		if strings.HasPrefix(base, "container-registry-") {
			return nil
		}
		if base == "container-registry-eject.bicep.tmpl" {
			return nil
		}
		switch base {
		case "main.arm.json", "existing-project.bicep",
			"existing-project-eject.bicep", "existing-project.arm.json",
			"container-registry.bicep", "foundry-project.bicep":
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
		wrapped["resourceTokenSalt"] = paramValue{Value: "${AZD_RESOURCE_TOKEN_SALT}"}
		wrapped["principalId"] = paramValue{Value: "${AZURE_PRINCIPAL_ID}"}
		wrapped["principalType"] = paramValue{Value: "${AZURE_PRINCIPAL_TYPE}"}
	}

	doc := map[string]any{
		"$schema": "https://schema.management.azure.com/" +
			"schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters":     wrapped,
	}

	filename := module + ".parameters.json"
	return writeJSONArtifact(infraDir, artifactRoot, filename, doc)
}

// writeEmbeddedTerraformTemplates copies the static *.tf files under the
// embedded templates/terraform/ root into infraDir (flat -- the module has no
// submodules) and returns the files written. On any error it removes the
// partial infraDir.
//
// container-registry.tf is copied only when includeAcr is true (an agent uses docker:);
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
		// container-registry.tf is omitted unless an agent uses docker:.
		if name == "container-registry.tf" && !includeAcr {
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

func writeExistingProjectTerraformTemplates(
	infraDir string,
	artifactRoot string,
	acrMode infraEjectAcrMode,
	acrPullAssigned bool,
) ([]ejectArtifact, error) {
	artifacts, err := writeTerraformTemplateSet(
		infraDir,
		artifactRoot,
		acrMode == infraEjectAcrCreate,
		"templates/terraform-existing-project",
		synthesis.ExistingProjectTerraformTemplatesFS(),
	)
	if err != nil {
		return nil, err
	}
	if acrMode == infraEjectAcrReuseConnect {
		source := "templates/terraform-existing-project/container-registry-reuse.tf"
		if acrPullAssigned {
			source = "templates/terraform-existing-project/container-registry-connect.tf"
		}
		artifact, err := copyTerraformTemplate(
			infraDir, artifactRoot, source,
			"container-registry.tf",
			synthesis.ExistingProjectTerraformTemplatesFS())
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func copyTerraformTemplate(
	infraDir, artifactRoot, source, target string,
	tfs fs.FS,
) (ejectArtifact, error) {
	data, err := fs.ReadFile(tfs, source)
	if err != nil {
		return ejectArtifact{}, infraInstallError("read Terraform template", err)
	}
	//nolint:gosec // generated Terraform is intended to be human-readable
	if err := os.WriteFile(filepath.Join(infraDir, target), data, 0o644); err != nil {
		return ejectArtifact{}, infraInstallError("write Terraform template", err)
	}
	return ejectArtifact{relPath: filepath.ToSlash(filepath.Join(artifactRoot, target)), bytes: len(data)}, nil
}

func writeTerraformTemplateSet(
	infraDir string,
	artifactRoot string,
	includeAcr bool,
	templatesRoot string,
	tfs fs.FS,
) (_ []ejectArtifact, retErr error) {
	//nolint:gosec // generated infrastructure must be readable by project tooling
	if err := os.MkdirAll(infraDir, 0o755); err != nil {
		return nil, infraInstallError("create Terraform infrastructure directory", err)
	}
	defer func() {
		if retErr != nil {
			_ = os.RemoveAll(infraDir)
		}
	}()
	entries, err := fs.ReadDir(tfs, templatesRoot)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("read Terraform templates: %s", err),
		)
	}
	var artifacts []ejectArtifact
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".tf") ||
			name == "container-registry-reuse.tf" || name == "container-registry-connect.tf" ||
			name == "container-registry-create.tf" {
			continue
		}
		data, err := fs.ReadFile(tfs, templatesRoot+"/"+name)
		if err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("read Terraform template %s: %s", name, err),
			)
		}
		//nolint:gosec // generated Terraform is intended to be human-readable
		if err := os.WriteFile(filepath.Join(infraDir, name), data, 0o644); err != nil {
			return nil, exterrors.Internal(
				exterrors.CodeInfraEjectWriteFailed,
				fmt.Sprintf("write Terraform template %s: %s", name, err),
			)
		}
		artifacts = append(artifacts, ejectArtifact{
			relPath: filepath.ToSlash(filepath.Join(artifactRoot, name)),
			bytes:   len(data),
		})
	}
	if includeAcr {
		artifact, err := copyTerraformTemplate(
			infraDir, artifactRoot, templatesRoot+"/container-registry-create.tf",
			"container-registry.tf", tfs)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
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
	return writeTerraformOutputsFile(
		infraDir,
		artifactRoot,
		includeAcr,
		"templates/terraform/outputs.tf.tmpl",
		synthesis.TerraformTemplatesFS(),
		layer,
		"",
	)
}

func writeTerraformOutputsFile(
	infraDir string,
	artifactRoot string,
	includeAcr bool,
	tmplPath string,
	tfs fs.FS,
	layer bool,
	acrMode string,
) (ejectArtifact, error) {
	raw, err := fs.ReadFile(tfs, tmplPath)
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
	if err := tmpl.Execute(&buf, struct {
		IncludeAcr bool
		Layer      bool
		AcrMode    string
	}{IncludeAcr: includeAcr, Layer: layer, AcrMode: acrMode}); err != nil {
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
		relPath: filepath.ToSlash(filepath.Join(artifactRoot, "outputs.tf")),
		bytes:   buf.Len(),
	}, nil
}

func writeExistingProjectTfvarsFile(
	infraDir string,
	artifactRoot string,
	module string,
	params map[string]any,
	environments []map[string]string,
) (ejectArtifact, error) {
	doc := map[string]any{ //nolint:gosec // environment placeholders, not credentials
		"subscription_id":     "${AZURE_SUBSCRIPTION_ID}",
		"tenant_id":           "${AZURE_TENANT_ID}",
		"project_resource_id": "${AZURE_AI_PROJECT_ID}",
		"project_endpoint":    "${FOUNDRY_PROJECT_ENDPOINT}",
		"location":            "${AZURE_LOCATION}",
		"resource_group_name": "${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}",
		"environment_name":    "${AZURE_ENV_NAME}",
		"resource_token_salt": "${AZD_RESOURCE_TOKEN_SALT}",
	}
	if len(environments) > 0 {
		doc["existing_acr_endpoint"] = environments[0]["AZURE_CONTAINER_REGISTRY_ENDPOINT"]
		doc["existing_acr_resource_id"] = environments[0]["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
		doc["existing_acr_connection_name"] = environments[0]["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"]
	}
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
	credentials, ok := params["connectionCredentials"].(map[string]map[string]any)
	if !ok {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("connectionCredentials parameter has unexpected type %T", params["connectionCredentials"]),
		)
	}
	doc["connections"] = synthesis.JoinConnectionCredentials(connections, credentials)
	return writeJSONArtifact(infraDir, artifactRoot, module+".tfvars.json", doc)
}

func readInfraEjectEnvironment(ctx context.Context, azdClient *azdext.AzdClient) (map[string]string, error) {
	current, err := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, fmt.Errorf("read active azd environment for infrastructure eject: %w", err)
	}
	if current == nil || current.Environment == nil || current.Environment.Name == "" {
		return nil, nil
	}
	values, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{Name: current.Environment.Name})
	if err != nil {
		return nil, fmt.Errorf("read active azd environment for infrastructure eject: %w", err)
	}
	result := make(map[string]string, len(values.KeyValues))
	for _, item := range values.KeyValues {
		result[item.Key] = item.Value
	}
	return result, nil
}

func validateExistingProjectEjectEnvironment(endpoint string, values map[string]string) error {
	envEndpoint := strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"])
	if envEndpoint == "" || !sameFoundryProject(endpoint, envEndpoint) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"FOUNDRY_PROJECT_ENDPOINT does not match the existing project configured in azure.yaml",
			"re-run `azd ai agent init` against the configured existing project",
		)
	}

	account, projectName := foundryEndpointIdentity(endpoint)
	idAccount, idProject := foundryResourceIDIdentity(values["AZURE_AI_PROJECT_ID"])
	if idAccount == "" || !strings.EqualFold(account, idAccount) || !strings.EqualFold(projectName, idProject) {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"AZURE_AI_PROJECT_ID does not match the existing project configured in azure.yaml",
			"re-run `azd ai agent init` against the configured existing project",
		)
	}
	return nil
}

func sameFoundryProject(a, b string) bool {
	aAccount, aProject := foundryEndpointIdentity(a)
	bAccount, bProject := foundryEndpointIdentity(b)
	return aAccount != "" && aProject != "" &&
		strings.EqualFold(aAccount, bAccount) && strings.EqualFold(aProject, bProject)
}

func foundryEndpointIdentity(endpoint string) (string, string) {
	const projectSegment = "/projects/"
	value := strings.TrimSpace(endpoint)
	_, hostAndPath, ok := strings.Cut(value, "://")
	if !ok {
		return "", ""
	}
	pathStart := strings.Index(hostAndPath, "/")
	if pathStart < 0 {
		return "", ""
	}
	host := strings.ToLower(hostAndPath[:pathStart])
	const hostSuffix = ".services.ai.azure.com"
	if !strings.HasSuffix(host, hostSuffix) {
		return "", ""
	}
	path := hostAndPath[pathStart:]
	projectStart := strings.Index(strings.ToLower(path), projectSegment)
	if projectStart < 0 {
		return "", ""
	}
	projectName := strings.Split(strings.Trim(path[projectStart+len(projectSegment):], "/"), "/")[0]
	return strings.TrimSuffix(host, hostSuffix), projectName
}

func foundryResourceIDIdentity(resourceID string) (string, string) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(resourceID), "/"), "/")
	if len(parts) != 10 || !strings.EqualFold(parts[0], "subscriptions") ||
		!strings.EqualFold(parts[2], "resourceGroups") || !strings.EqualFold(parts[4], "providers") ||
		!strings.EqualFold(parts[5], "Microsoft.CognitiveServices") || !strings.EqualFold(parts[6], "accounts") ||
		!strings.EqualFold(parts[8], "projects") {
		return "", ""
	}
	return parts[7], parts[9]
}

func resolveInfraEjectAcrMode(params map[string]any, values map[string]string) (infraEjectAcrMode, error) {
	includeAcr, _ := params["includeAcr"].(bool)
	if !includeAcr || strings.EqualFold(strings.TrimSpace(values["AZD_AGENT_SKIP_ACR"]), "true") {
		return infraEjectAcrNone, nil
	}
	mode := infraEjectAcrMode(strings.TrimSpace(values["AZD_FOUNDRY_ACR_MODE"]))
	if mode != "" {
		switch mode {
		case infraEjectAcrNone, infraEjectAcrCreate, infraEjectAcrReuseConnect, infraEjectAcrAlreadyConnected:
		default:
			return "", exterrors.Validation(
				exterrors.CodeInvalidParameter,
				fmt.Sprintf("AZD_FOUNDRY_ACR_MODE has unsupported value %q", mode),
				"re-run `azd ai agent init` to select the container registry behavior",
			)
		}
	}
	endpoint := strings.TrimSpace(values["AZURE_CONTAINER_REGISTRY_ENDPOINT"])
	resourceID := strings.TrimSpace(values["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"])
	connection := strings.TrimSpace(values["AZURE_AI_PROJECT_ACR_CONNECTION_NAME"])
	if mode == infraEjectAcrReuseConnect || mode == infraEjectAcrAlreadyConnected {
		if endpoint == "" || resourceID == "" {
			return "", exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf("%s requires both container registry endpoint and resource ID", mode),
				"set AZURE_CONTAINER_REGISTRY_ENDPOINT and AZURE_CONTAINER_REGISTRY_RESOURCE_ID, then retry",
			)
		}
		if mode == infraEjectAcrAlreadyConnected && connection == "" {
			return "", exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				"already-connected requires AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
				"set AZURE_AI_PROJECT_ACR_CONNECTION_NAME to the existing project connection, then retry",
			)
		}
		return mode, nil
	}
	if mode != "" {
		return mode, nil
	}
	if endpoint == "" && resourceID == "" && connection == "" {
		return infraEjectAcrCreate, nil
	}
	if endpoint == "" || resourceID == "" {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"existing container registry state is incomplete",
			"set both AZURE_CONTAINER_REGISTRY_ENDPOINT and AZURE_CONTAINER_REGISTRY_RESOURCE_ID, or clear both",
		)
	}
	if connection == "" {
		return infraEjectAcrReuseConnect, nil
	}
	return infraEjectAcrAlreadyConnected, nil
}

// writeTfvarsFile emits infra/main.tfvars.json. azd-core's Terraform provider
// reads this file and substitutes the ${...} placeholders from the azd
// environment at provision time. The synthesizer-known values `deployments`
// and `connections` are written literally; deploy-time inputs (location,
// resource_group_name, foundry_project_name, principal_id, subscription_id,
// environment_name, resource_token_salt) are left as azd environment placeholders.
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
	doc := map[string]any{ //nolint:gosec // G101: environment placeholder names, not credentials
		"subscription_id":      "${AZURE_SUBSCRIPTION_ID}",
		"location":             "${AZURE_LOCATION}",
		"resource_group_name":  "${AZURE_RESOURCE_GROUP}",
		"environment_name":     "${AZURE_ENV_NAME}",
		"foundry_project_name": "${AZURE_AI_PROJECT_NAME}",
		"principal_id":         "${AZURE_PRINCIPAL_ID}",
		"resource_token_salt":  "${AZD_RESOURCE_TOKEN_SALT}",
	}
	if layer {
		doc["resource_group_name"] = "${AZURE_FOUNDRY_RESOURCE_GROUP=rg-${AZURE_ENV_NAME}-foundry}"
		doc["foundry_project_name"] = "${AZURE_AI_PROJECT_NAME=}"
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

	filename := module + ".tfvars.json"
	return writeJSONArtifact(infraDir, artifactRoot, filename, doc)
}

func writeJSONArtifact(infraDir, artifactRoot, filename string, value any) (ejectArtifact, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("marshal %s: %s", filename, err),
		)
	}
	// json.MarshalIndent omits a trailing newline; add one for editors/POSIX tools.
	data = append(data, '\n')

	dst := filepath.Join(infraDir, filename)
	//nolint:gosec // G306: ejected JSON is intended to be human-readable
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return ejectArtifact{}, exterrors.Internal(
			exterrors.CodeInfraEjectWriteFailed,
			fmt.Sprintf("write %s: %s", filename, err),
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
