// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"

	"azureaiagent/internal/pkg/agents/agent_yaml"
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

// populateManifestResources walks each service's agent.manifest.yaml
// (when present) and aggregates the declared model/toolbox/connection
// resources onto state. The walker is strictly best-effort: missing
// files, unreadable bytes, malformed YAML, and unknown resource kinds
// are all silently skipped so an in-flight `azd ai agent init` (which
// rewrites the manifest mid-flight) or a template with no manifest
// (e.g., a bare agent.yaml) never blocks the rest of state assembly.
//
// Aggregation rules:
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
func populateManifestResources(projectPath string, state *State) {
	if state == nil || projectPath == "" || len(state.Services) == 0 {
		return
	}

	models := map[resourceKey]ResourceRef{}
	toolboxes := map[resourceKey]ResourceRef{}
	connections := map[resourceKey]ResourceRef{}

	for _, svc := range state.Services {
		data := readManifestBytes(projectPath, svc.RelativePath)
		if data == nil {
			continue
		}
		resources, err := agent_yaml.ExtractResourceDefinitions(data)
		if err != nil {
			continue
		}
		for _, resource := range resources {
			switch r := resource.(type) {
			case agent_yaml.ModelResource:
				if r.Name == "" {
					continue
				}
				k := resourceKey{service: svc.Name, name: r.Name}
				if _, dup := models[k]; dup {
					continue
				}
				models[k] = ResourceRef{
					Name:        r.Name,
					ServiceName: svc.Name,
					Detail:      r.Id,
				}
			case agent_yaml.ToolboxResource:
				if r.Name == "" {
					continue
				}
				k := resourceKey{service: svc.Name, name: r.Name}
				if _, dup := toolboxes[k]; dup {
					continue
				}
				toolboxes[k] = ResourceRef{
					Name:        r.Name,
					ServiceName: svc.Name,
				}
			case agent_yaml.ConnectionResource:
				if r.Name == "" {
					continue
				}
				k := resourceKey{service: svc.Name, name: r.Name}
				if _, dup := connections[k]; dup {
					continue
				}
				connections[k] = ResourceRef{
					Name:        r.Name,
					ServiceName: svc.Name,
					Detail:      connectionDetail(r),
				}
			}
		}
	}

	state.ModelRefs = sortedResourceRefs(models)
	state.Toolboxes = sortedResourceRefs(toolboxes)
	state.Connections = sortedResourceRefs(connections)
	state.HasModels = len(state.ModelRefs) > 0
	state.HasToolboxes = len(state.Toolboxes) > 0
	state.HasConnections = len(state.Connections) > 0
}

// readManifestBytes returns the first manifest file's contents under
// `<projectPath>/<relativePath>/` (probing the names in
// manifestFileNames order) or nil if none exists / is readable. All
// failure modes — empty paths, missing directory, permission errors,
// truly empty file — return nil because every doctor / resolver
// consumer treats nil as "no manifest discovered for this service"
// and degrades gracefully.
func readManifestBytes(projectPath, relativePath string) []byte {
	if projectPath == "" || relativePath == "" {
		return nil
	}
	for _, name := range manifestFileNames {
		path := filepath.Join(projectPath, relativePath, name)
		//nolint:gosec // G304: path constructed from azd project root, not user input.
		data, err := os.ReadFile(path)
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
func connectionDetail(r agent_yaml.ConnectionResource) string {
	category := string(r.Category)
	target := r.Target
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
