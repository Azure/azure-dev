// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"go.yaml.in/yaml/v3"
)

// ACRRemoteBuildRuleID identifies the remote registry build check.
const ACRRemoteBuildRuleID = "azure.ai.projects.acr_remote_build"

const acrRemoteBuildSuggestion = "set docker.remoteBuild: false on source-built agent services and use local Docker " +
	"with Container Registry Repository Writer push permission, use a prebuilt image, " +
	"or provision a new environment with a classic RBAC registry"

// ACRRemoteBuildCheck rejects unsupported remote source builds.
type ACRRemoteBuildCheck struct {
	azdClient  *azdext.AzdClient
	acrRequest func(context.Context, string, string, string, any) error
	compiler   bicepCompiler
}

// NewACRRemoteBuildCheck checks source-build registry compatibility.
func NewACRRemoteBuildCheck(client *azdext.AzdClient) *ACRRemoteBuildCheck {
	return &ACRRemoteBuildCheck{azdClient: client}
}

// Validate checks ACR build support before provision initialization.
func (c *ACRRemoteBuildCheck) Validate(
	ctx context.Context, _ *azdext.ValidationContext, _ *azdext.ValidationCheckRequest,
) (*azdext.ValidationCheckResponse, error) {
	project, err := c.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return acrBuildCheckFailure(fmt.Errorf("read project before registry build validation: %w", err)), nil
	}
	if _, _, usesFoundry, err := acrBuildProjectConfig(project.GetProject()); err != nil {
		return acrBuildCheckFailure(err), nil
	} else if !usesFoundry {
		return &azdext.ValidationCheckResponse{}, nil
	}
	p := &FoundryProvisioningProvider{azdClient: c.azdClient}
	values, err := p.synthesisEnvMap(ctx)
	if err != nil {
		return acrBuildCheckFailure(err), nil
	}
	err = c.ValidateProject(ctx, project.GetProject(), p.envName, values)
	if err != nil {
		return acrBuildCheckFailure(err), nil
	}
	return &azdext.ValidationCheckResponse{}, nil
}

func acrBuildCheckFailure(err error) *azdext.ValidationCheckResponse {
	return &azdext.ValidationCheckResponse{Results: []*azdext.ValidationCheckResult{{
		Severity:     azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR,
		DiagnosticId: ACRRemoteBuildRuleID,
		Message:      err.Error(),
		Suggestion:   azdext.ErrorSuggestion(err),
	}}}
}

// ValidateProject is also a hard guard before lifecycle state writes.
func (c *ACRRemoteBuildCheck) ValidateProject(
	ctx context.Context, project *azdext.ProjectConfig, envName string, values map[string]string,
) error {
	raw, config, usesFoundry, err := acrBuildProjectConfig(project)
	if err != nil || !usesFoundry {
		return err
	}
	snapshot := map[string]string{}
	maps.Copy(snapshot, values)
	snapshot["AZURE_ENV_NAME"] = envName
	in := synthesis.Input{
		RawAzureYAML: raw, Env: snapshot, ProjectRoot: project.Path,
		AcceptedHosts: FoundryProvisioningServiceHosts, PreserveVarRefs: true,
	}
	remote, err := synthesis.RequiresRemoteACRBuild(in)
	if err != nil {
		return err
	}
	if err := synthesis.RequireACRReuseForEnvironment(ctx, c.azdClient, envName, synthesis.Input{
		RawAzureYAML: raw, Env: snapshot, ProjectRoot: project.Path,
	}); err != nil {
		return err
	}
	if !remote {
		return nil
	}
	in.ServiceName, err = findFoundryProjectService(raw)
	if err != nil {
		return err
	}
	endpoint, err := resolvedFoundryServiceEndpointAtRoot(raw, project.Path, in.ServiceName, snapshot)
	if err != nil || endpoint != "" {
		return err
	}
	p := &FoundryProvisioningProvider{
		azdClient: c.azdClient, projectPath: project.Path, envName: envName,
		projectYAML:  raw,
		synthesisEnv: snapshot, acrRemoteBuild: true,
		acrRequest: c.acrRequest, bicepCliInstance: c.compiler,
		subID: strings.TrimSpace(values[envKeySubscriptionID]), location: strings.TrimSpace(values[envKeyLocation]),
		foundryName: strings.TrimSpace(values[envKeyProjectName]),
	}
	if err := p.loadACRLifecycle(ctx); err != nil {
		return err
	}
	in.ACRBinding = p.acrBinding
	mode, id, err := synthesis.ResolveGreenfieldACRSelection(in)
	if err != nil {
		return err
	}
	p.rgName, err = synthesis.GreenfieldACRResourceGroup(in)
	if err != nil {
		return err
	}
	if p.foundryName == "" {
		p.foundryName = sanitizeFoundryName(envName)
	}
	var infra struct {
		Infra struct {
			Path   string `yaml:"path"`
			Module string `yaml:"module"`
			Layers []struct {
				Name   string `yaml:"name"`
				Path   string `yaml:"path"`
				Module string `yaml:"module"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	if err := yaml.Unmarshal(raw, &infra); err != nil {
		return err
	}
	path, module := infra.Infra.Path, infra.Infra.Module
	for _, layer := range infra.Infra.Layers {
		if config.hasFoundryLayer(layer.Name) {
			path, module = layer.Path, layer.Module
		}
	}
	if path == "" {
		path = onDiskInfraDir
	}
	if !filepath.IsLocal(path) || (module != "" && filepath.Base(module) != module) {
		return errors.New("Foundry Bicep path and module must be project-relative")
	}
	p.infraPath, p.infraModule = filepath.Join(project.Path, path), module
	src := &templateSource{parameters: map[string]any{
		"includeAcr": map[string]any{"value": mode == "reuse-connect"},
	}}
	if mode == "reuse-connect" {
		src.parameters["existingAcrResourceId"] = map[string]any{"value": id}
	}
	onDisk := p.onDiskTemplatePresent()
	if onDisk {
		in.DeferACRNetworkValidation = true
		result, err := synthesis.Synthesize(in)
		if err != nil {
			return err
		}
		p.synthResult = result
		if mode == "reuse-connect" {
			p.synthResult.Parameters["existingAcrResourceId"] = id
		}
		src, err = p.resolveTemplateSource(ctx, func(string) {})
		if err != nil {
			return err
		}
	}
	id, _ = parameterValue(src, "existingAcrResourceId").(string)
	if p.subID == "" && id != "" {
		if err := synthesis.ValidateACRSelector(id, "", ""); err != nil {
			return err
		}
		p.subID = strings.Split(id, "/")[2]
	}
	if onDisk {
		if err := p.validateACRTemplate(src); err != nil {
			return err
		}
	}
	return p.validateACRRemoteBuild(ctx, src)
}

func acrBuildProjectConfig(project *azdext.ProjectConfig) ([]byte, foundryInfraConfig, bool, error) {
	if project.GetPath() == "" {
		return nil, foundryInfraConfig{}, false, nil
	}
	raw, _, err := readProjectFile(project.Path)
	if err != nil {
		return nil, foundryInfraConfig{}, false, err
	}
	config, err := parseFoundryInfraConfig(raw)
	if err != nil {
		return nil, config, false, err
	}
	if config.rootProvider == "" {
		config.rootProvider = project.GetInfra().GetProvider()
	}
	return raw, config, config.rootProvider == FoundryProviderName || len(config.foundryLayers) > 0, nil
}

func (p *FoundryProvisioningProvider) validateACRRemoteBuild(ctx context.Context, src *templateSource) error {
	id, _ := parameterValue(src, "existingAcrResourceId").(string)
	if id == "" && p.acrBinding != nil && p.acrBinding.Mode == "reuse-connect" {
		id = p.acrBinding.RegistryResourceID
	}
	if !p.acrRemoteBuild || p.brownfieldEndpoint != "" || id == "" {
		return nil
	}
	if err := synthesis.RequireACRReuse(); err != nil {
		return err
	}
	group, _ := parameterValue(src, "resourceGroupName").(string)
	if group == "" {
		group = p.rgName
	}
	if err := p.validateACRTeardownScopes(id, group); err != nil {
		return err
	}
	abac, err := p.acrUsesABAC(ctx, id)
	if err != nil {
		return err
	}
	return p.validateACRRemoteBuildMode(abac)
}

func (p *FoundryProvisioningProvider) acrUsesABAC(ctx context.Context, id string) (bool, error) {
	var registry acrResource
	if err := p.acrARM(ctx, http.MethodGet, id, "2025-11-01", &registry); err != nil {
		return false, fmt.Errorf("read external registry permission mode: %w", err)
	}
	mode := registry.Properties.RoleAssignmentMode
	abac := strings.EqualFold(mode, "AbacRepositoryPermissions")
	if !abac && mode != "" && !strings.EqualFold(mode, "LegacyRegistryPermissions") {
		return false, errors.New("external registry has an unsupported role assignment mode")
	}
	return abac, nil
}

func (p *FoundryProvisioningProvider) validateACRRemoteBuildMode(abac bool) error {
	if p.acrRemoteBuild && abac {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"ABAC external registries do not support hosted source builds with docker.remoteBuild: true: "+
				"the current ACR Quick Build request does not authenticate the source registry",
			acrRemoteBuildSuggestion,
		)
	}
	return nil
}
