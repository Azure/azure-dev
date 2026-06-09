// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// agentServiceConfig is the slim projection of an azd service's `config:`
// block (azure.yaml `services.<name>.config`) that nextstep resolvers and
// doctor checks consume: the names of declared model deployments,
// toolboxes, and connections.
//
// The authoritative, full schema lives in project.ServiceTargetAgentConfig,
// but nextstep cannot import that package — `internal/project` imports
// `internal/cmd/nextstep`, so the reverse import would close a cycle. This
// local copy therefore mirrors only the fields read here and is unmarshaled
// directly from the gRPC-provided structpb config. Add fields only when a
// resolver branch or doctor check needs them.
//
// `config.toolConnections[]` is deliberately omitted: it has no equivalent
// in the legacy manifest `resources[]` block (those connections were derived
// from tool definitions during init, not declared as `kind: connection`), so
// surfacing them in state.Connections would expand the doctor connection
// check beyond its prior scope. Add a ToolConnections field here only if that
// behavior change is intended.
type agentServiceConfig struct {
	Deployments []agentDeployment `json:"deployments"`
	Toolboxes   []agentToolbox    `json:"toolboxes"`
	Connections []agentConnection `json:"connections"`
}

// agentDeployment mirrors one entry of `config.deployments[]`. Name is the
// deployment name agents reference (e.g. via AZURE_AI_MODEL_DEPLOYMENT_NAME);
// Model carries the underlying model identity used to render the resource
// Detail.
type agentDeployment struct {
	Name  string `json:"name"`
	Model struct {
		Name    string `json:"name"`
		Format  string `json:"format"`
		Version string `json:"version"`
	} `json:"model"`
}

// agentToolbox mirrors one entry of `config.toolboxes[]`. Only the name is
// consumed — it seeds the canonical TOOLBOX_<NAME>_MCP_ENDPOINT env key the
// toolbox-endpoint partition and doctor check look up.
type agentToolbox struct {
	Name string `json:"name"`
}

// agentConnection mirrors one entry of `config.connections[]`. Category and
// Target feed the kind-specific Detail rendered in doctor remediation lines.
type agentConnection struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Target   string `json:"target"`
}

// populateServiceResources aggregates the model/toolbox/connection resources
// declared in each agent service's `config:` block (azure.yaml) onto state.
// The config is read from the gRPC-provided ServiceConfig.Config that the
// project snapshot already carries — no manifest files are read from disk.
//
// Parsing is strictly best-effort: a nil config, malformed structpb, or a
// service with no `config:` block is silently skipped so an in-flight
// `azd ai agent init` (which writes azure.yaml incrementally) never blocks
// the rest of state assembly.
//
// Aggregation rules:
//
//   - Has* flags are true when at least one resource of the matching kind is
//     declared across all agent services.
//   - Slices are sorted by Name (ties broken by ServiceName) and the pair
//     (ServiceName, Name) is the de-duplication key — the same name declared
//     by two services surfaces twice; the same name listed twice under one
//     service collapses to one entry. This matches the doctor-check
//     expectation that per-service failures remain individually addressable.
//   - The Detail field carries a kind-specific summary (model identity,
//     connection category/target, empty for toolboxes) so doctor remediation
//     lines have enough context to be actionable without re-parsing config.
func populateServiceResources(project *azdext.ProjectConfig, state *State) {
	if state == nil || project == nil || len(project.Services) == 0 {
		return
	}

	models := map[resourceKey]ResourceRef{}
	toolboxes := map[resourceKey]ResourceRef{}
	connections := map[resourceKey]ResourceRef{}

	for _, svc := range project.Services {
		if svc == nil || svc.Host != agentHost || svc.Config == nil {
			continue
		}
		cfg, err := parseAgentServiceConfig(svc.Config)
		if err != nil {
			continue
		}

		for _, d := range cfg.Deployments {
			if d.Name == "" {
				continue
			}
			k := resourceKey{service: svc.Name, name: d.Name}
			if _, dup := models[k]; dup {
				continue
			}
			models[k] = ResourceRef{
				Name:        d.Name,
				ServiceName: svc.Name,
				Detail:      modelDetail(d),
			}
		}

		for _, tb := range cfg.Toolboxes {
			if tb.Name == "" {
				continue
			}
			k := resourceKey{service: svc.Name, name: tb.Name}
			if _, dup := toolboxes[k]; dup {
				continue
			}
			toolboxes[k] = ResourceRef{
				Name:        tb.Name,
				ServiceName: svc.Name,
			}
		}

		for _, conn := range cfg.Connections {
			if conn.Name == "" {
				continue
			}
			k := resourceKey{service: svc.Name, name: conn.Name}
			if _, dup := connections[k]; dup {
				continue
			}
			connections[k] = ResourceRef{
				Name:        conn.Name,
				ServiceName: svc.Name,
				Detail:      connectionDetail(conn.Category, conn.Target),
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

// parseAgentServiceConfig converts a service's structpb `config:` block into
// the slim agentServiceConfig projection. The structpb is round-tripped
// through protojson so nested arrays/objects decode the same way the deploy
// path decodes them (project.UnmarshalStruct uses the identical pattern).
func parseAgentServiceConfig(s *structpb.Struct) (*agentServiceConfig, error) {
	data, err := protojson.Marshal(s)
	if err != nil {
		return nil, err
	}
	var cfg agentServiceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// modelDetail renders the kind-specific identifier doctor remediation
// messages quote for a model deployment: the underlying model name plus its
// version when both are present (e.g. "gpt-4.1 (2025-04-14)"), falling back
// to whichever side is populated so the result is never an empty
// parenthetical.
func modelDetail(d agentDeployment) string {
	switch {
	case d.Model.Name != "" && d.Model.Version != "":
		return d.Model.Name + " (" + d.Model.Version + ")"
	case d.Model.Name != "":
		return d.Model.Name
	default:
		return d.Model.Version
	}
}

// connectionDetail renders the kind-specific identifier doctor remediation
// messages quote when a connection is missing or misconfigured. Empty
// category or target falls back to whichever side is populated so we never
// emit a useless " | " separator with both halves blank.
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

// resourceKey is the (service, name) dedup key for the per-kind resource maps
// populated by populateServiceResources. Declared at package level so
// sortedResourceRefs can name the map type explicitly in its signature
// without a divergent anonymous-struct re-declaration.
type resourceKey struct {
	service string
	name    string
}

// sortedResourceRefs flattens the dedup map into a slice sorted by Name (ties
// broken by ServiceName). Callers consume the result by iterating in order,
// so the determinism is load-bearing for both doctor output snapshots and
// downstream display.
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
