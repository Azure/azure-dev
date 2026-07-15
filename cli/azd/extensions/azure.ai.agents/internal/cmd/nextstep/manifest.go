// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"cmp"
	"fmt"
	"os"
	"slices"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/paths"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// manifestFileNames are the candidate manifest filenames the walker
// probes, in the same precedence order init / deploy paths use:
// agent.manifest.yaml wins over agent.manifest.yml. The non-manifest
// agent.yaml is deliberately NOT in this list — that file describes
// the running container (env vars, protocols) and never declares
// resources; mistakenly walking it would surface zero resources for
// every service that uses only agent.yaml (init-pending or
// agent.yaml-only templates).
var manifestFileNames = []string{
	"agent.manifest.yaml",
	"agent.manifest.yml",
}

const (
	aiProjectHost    = "azure.ai.project"
	aiConnectionHost = "azure.ai.connection"
	aiToolboxHost    = "azure.ai.toolbox"
)

// populateResources reads unified resources with legacy fallbacks.
//
// Unified split services take precedence over bundled service config.
// Bundled config takes precedence over legacy manifest files.
//
//   - Has* flags are true when at least one resource of the matching
//     kind is found across all services.
//   - Slices are sorted by Name (ties broken by ServiceName) and the
//     pair (ServiceName, Name) is the de-duplication key — the same
//     name appearing under two services surfaces twice; the same name
//     listed twice in one service collapses to one entry. This
//     matches the doctor-check expectation that per-service failures
//     remain individually addressable.
//   - The Detail field carries a kind-specific summary (model id,
//     connection target/category, empty for toolboxes) so doctor
//     remediation lines have enough context to be actionable without
//     re-parsing the manifest.
//
// Resource enumeration uses agent_yaml.ExtractResourceDefinitions
// directly (rather than LoadAndValidateAgentManifest) so a manifest
// with an absent / partial `template` block — common during init —
// still surfaces its `resources:` declarations.
func populateResources(
	projectConfig *azdext.ProjectConfig,
	state *State,
	errs *[]error,
) {
	if projectConfig == nil || state == nil {
		return
	}

	resources := newResourceSets()
	collectSplitResources(
		projectConfig,
		&resources,
		errs,
	)
	collectBundledResources(
		projectConfig,
		&resources,
		errs,
	)
	collectManifestResources(
		projectConfig.Path,
		state.Services,
		&resources,
		errs,
	)
	applyResourceSets(state, resources)
}

type resourceSets struct {
	models           map[resourceKey]ResourceRef
	toolboxes        map[resourceKey]ResourceRef
	connections      map[resourceKey]ResourceRef
	modelErrors      []string
	toolboxErrors    []string
	connectionErrors []string
}

func newResourceSets() resourceSets {
	return resourceSets{
		models:      map[resourceKey]ResourceRef{},
		toolboxes:   map[resourceKey]ResourceRef{},
		connections: map[resourceKey]ResourceRef{},
	}
}

func collectSplitResources(
	projectConfig *azdext.ProjectConfig,
	resources *resourceSets,
	errs *[]error,
) {
	for _, svc := range sortedProjectServices(projectConfig.Services) {
		switch svc.GetHost() {
		case aiProjectHost:
			props, err := resolveServiceConfigProps(
				svc,
				projectConfig.Path,
			)
			if err != nil {
				appendResourceError(
					errs,
					&resources.modelErrors,
					svc,
					err,
				)
				continue
			}
			var cfg guidanceServiceConfig
			if props != nil {
				if err := unmarshalServiceProps(
					props,
					&cfg,
				); err != nil {
					appendResourceError(
						errs,
						&resources.modelErrors,
						svc,
						err,
					)
					continue
				}
			}
			for _, deployment := range cfg.Deployments {
				addResource(
					resources.models,
					ResourceRef{
						Name:        deployment.Name,
						ServiceName: svc.GetName(),
						Detail:      deployment.Model.Name,
					},
				)
			}
		case aiConnectionHost:
			props, err := resolveServiceConfigProps(
				svc,
				projectConfig.Path,
			)
			if err != nil {
				appendResourceError(
					errs,
					&resources.connectionErrors,
					svc,
					err,
				)
				continue
			}
			var connection guidanceConnection
			if props != nil {
				if err := unmarshalServiceProps(
					props,
					&connection,
				); err != nil {
					appendResourceError(
						errs,
						&resources.connectionErrors,
						svc,
						err,
					)
					continue
				}
			}
			connection.Name = svc.GetName()
			addResource(
				resources.connections,
				ResourceRef{
					Name:        connection.Name,
					ServiceName: svc.GetName(),
					Detail: connectionDetail(
						connection.Category,
						connection.Target,
					),
				},
			)
		case aiToolboxHost:
			if _, err := resolveServiceConfigProps(
				svc,
				projectConfig.Path,
			); err != nil {
				appendResourceError(
					errs,
					&resources.toolboxErrors,
					svc,
					err,
				)
				continue
			}
			addResource(
				resources.toolboxes,
				ResourceRef{
					Name:            svc.GetName(),
					ServiceName:     svc.GetName(),
					ManagedByDeploy: true,
				},
			)
		}
	}
}

func collectBundledResources(
	projectConfig *azdext.ProjectConfig,
	resources *resourceSets,
	errs *[]error,
) {
	needsModels := len(resources.models) == 0
	needsToolboxes := len(resources.toolboxes) == 0
	needsConnections := len(resources.connections) == 0
	if !needsModels && !needsToolboxes && !needsConnections {
		return
	}

	for _, svc := range sortedProjectServices(projectConfig.Services) {
		if svc.GetHost() != agentHost {
			continue
		}
		_, cfg, _, err := loadGuidanceServiceConfig(
			svc,
			projectConfig.Path,
		)
		if err != nil {
			appendResourceLoadError(
				errs,
				resources,
				svc.GetName(),
				err,
				needsModels,
				needsToolboxes,
				needsConnections,
			)
			continue
		}
		if needsModels {
			for _, deployment := range cfg.Deployments {
				addResource(
					resources.models,
					ResourceRef{
						Name:        deployment.Name,
						ServiceName: svc.GetName(),
						Detail:      deployment.Model.Name,
					},
				)
			}
		}
		if needsToolboxes {
			for _, toolbox := range cfg.Toolboxes {
				addResource(
					resources.toolboxes,
					ResourceRef{
						Name:        toolbox.Name,
						ServiceName: svc.GetName(),
					},
				)
			}
		}
		if needsConnections {
			for _, connection := range cfg.Connections {
				addResource(
					resources.connections,
					ResourceRef{
						Name:        connection.Name,
						ServiceName: svc.GetName(),
						Detail: connectionDetail(
							connection.Category,
							connection.Target,
						),
					},
				)
			}
		}
	}
}

func collectManifestResources(
	projectPath string,
	services []ServiceState,
	resources *resourceSets,
	errs *[]error,
) {
	needsModels := len(resources.models) == 0
	needsToolboxes := len(resources.toolboxes) == 0
	needsConnections := len(resources.connections) == 0
	if projectPath == "" ||
		(!needsModels && !needsToolboxes && !needsConnections) {
		return
	}

	for _, svc := range services {
		data := readManifestBytes(projectPath, svc.RelativePath)
		if data == nil {
			continue
		}
		definitions, err := agent_yaml.ExtractResourceDefinitions(data)
		if err != nil {
			appendResourceLoadError(
				errs,
				resources,
				svc.Name,
				err,
				needsModels,
				needsToolboxes,
				needsConnections,
			)
			continue
		}
		for _, definition := range definitions {
			switch r := definition.(type) {
			case agent_yaml.ModelResource:
				if !needsModels || r.Name == "" {
					continue
				}
				addResource(resources.models, ResourceRef{
					Name:        r.Name,
					ServiceName: svc.Name,
					Detail:      r.Id,
				})
			case agent_yaml.ToolboxResource:
				if !needsToolboxes || r.Name == "" {
					continue
				}
				addResource(resources.toolboxes, ResourceRef{
					Name:        r.Name,
					ServiceName: svc.Name,
				})
			case agent_yaml.ConnectionResource:
				if !needsConnections || r.Name == "" {
					continue
				}
				addResource(resources.connections, ResourceRef{
					Name:        r.Name,
					ServiceName: svc.Name,
					Detail: connectionDetail(
						string(r.Category),
						r.Target,
					),
				})
			}
		}
	}
}

func applyResourceSets(state *State, resources resourceSets) {
	state.ModelRefs = sortedResourceRefs(resources.models)
	state.Toolboxes = sortedResourceRefs(resources.toolboxes)
	state.Connections = sortedResourceRefs(resources.connections)
	state.HasModels = len(state.ModelRefs) > 0
	state.HasToolboxes = len(state.Toolboxes) > 0
	state.HasConnections = len(state.Connections) > 0
	state.ModelLoadErrors = slices.Clone(resources.modelErrors)
	state.ToolboxLoadErrors = slices.Clone(resources.toolboxErrors)
	state.ConnectionLoadErrors = slices.Clone(
		resources.connectionErrors,
	)
}

func addResource(
	resources map[resourceKey]ResourceRef,
	resource ResourceRef,
) {
	if resource.Name == "" {
		return
	}
	key := resourceKey{
		service: resource.ServiceName,
		name:    resource.Name,
	}
	if _, exists := resources[key]; exists {
		return
	}
	resources[key] = resource
}

func appendResourceError(
	errs *[]error,
	loadErrors *[]string,
	svc *azdext.ServiceConfig,
	err error,
) {
	wrapped := fmt.Errorf(
		"load resources for %s: %w",
		svc.GetName(),
		err,
	)
	*loadErrors = append(*loadErrors, wrapped.Error())
	if errs != nil {
		*errs = append(*errs, wrapped)
	}
}

func appendResourceLoadError(
	errs *[]error,
	resources *resourceSets,
	serviceName string,
	err error,
	models bool,
	toolboxes bool,
	connections bool,
) {
	wrapped := fmt.Errorf(
		"load resources for %s: %w",
		serviceName,
		err,
	)
	if errs != nil {
		*errs = append(*errs, wrapped)
	}
	if models {
		resources.modelErrors = append(
			resources.modelErrors,
			wrapped.Error(),
		)
	}
	if toolboxes {
		resources.toolboxErrors = append(
			resources.toolboxErrors,
			wrapped.Error(),
		)
	}
	if connections {
		resources.connectionErrors = append(
			resources.connectionErrors,
			wrapped.Error(),
		)
	}
}

func sortedProjectServices(
	services map[string]*azdext.ServiceConfig,
) []*azdext.ServiceConfig {
	out := make([]*azdext.ServiceConfig, 0, len(services))
	for _, svc := range services {
		if svc != nil {
			out = append(out, svc)
		}
	}
	slices.SortFunc(out, func(a, b *azdext.ServiceConfig) int {
		return cmp.Compare(a.GetName(), b.GetName())
	})
	return out
}

// readManifestBytes returns the first manifest file's contents under
// `<projectPath>/<relativePath>/` (probing the names in
// manifestFileNames order) or nil if none exists / is readable. All
// failure modes — empty paths, missing directory, permission errors,
// truly empty file — return nil because every doctor / resolver
// consumer treats nil as "no manifest discovered for this service"
// and degrades gracefully.
func readManifestBytes(projectPath, relativePath string) []byte {
	if projectPath == "" {
		return nil
	}
	for _, name := range manifestFileNames {
		manifestPath, err := paths.JoinAllowRoot(projectPath, relativePath, name)
		if err != nil {
			return nil
		}
		data, err := os.ReadFile(manifestPath) //nolint:gosec // path is validated under the project root
		if err == nil && len(data) > 0 {
			return data
		}
	}
	return nil
}

// connectionDetail renders the kind-specific identifier doctor
// remediation messages quote when a connection is missing or
// misconfigured. Empty-category and empty-target manifests fall back
// to whichever side is populated so we never emit a useless
// " | " separator with both halves blank.
func connectionDetail(category, target string) string {
	switch {
	case category != "" && target != "":
		return category + " | " + target
	case category != "":
		return category
	default:
		return target
	}
}

// resourceKey is the (service, name) dedup key for the per-kind
// resource maps populated by populateManifestResources. Declared at
// package level so sortedResourceRefs can name the map type
// explicitly in its signature without a divergent anonymous-struct
// re-declaration.
type resourceKey struct {
	service string
	name    string
}

// sortedResourceRefs flattens the dedup map into a slice sorted by
// Name (ties broken by ServiceName). Callers consume the result by
// iterating in order, so the determinism is load-bearing for both
// doctor output snapshots and downstream display.
func sortedResourceRefs(m map[resourceKey]ResourceRef) []ResourceRef {
	if len(m) == 0 {
		return nil
	}
	out := make([]ResourceRef, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b ResourceRef) int {
		if c := cmp.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.ServiceName, b.ServiceName)
	})
	return out
}
