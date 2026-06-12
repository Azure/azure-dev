// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package nextstep

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"azureaiagent/internal/pkg/paths"

	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// foundry_config.go is the single decode seam for the unified azure.yaml
// design (host: microsoft.foundry). Everything the nextstep package knows
// about the on-disk/over-the-wire shape of a Foundry service lives here, so
// that when the upstream design is finalized the rest of the package
// (state assembly, resolvers, doctor consumers) is insulated from
// field-shape churn — only the structs below and decodeFoundryService need
// to change.
//
// Data flow (per the engineering spec, PR #8590): core azd parses
// azure.yaml and forwards each service's top-level non-standard keys
// (endpoint, deployments, connections, toolboxes, skills, routines, agents)
// verbatim in ServiceConfig.AdditionalProperties as a structpb.Struct (no
// env substitution applied). We round-trip that struct through JSON, then
// resolve any data-side `$ref:` file includes — which core does NOT expand
// — by reading the referenced sibling YAML file from disk and applying the
// spec's overlay + path-rebasing rules.
//
// nextstep is a leaf package (internal/project imports it), so the model is
// defined locally here rather than reusing internal/project types — reusing
// them would create an import cycle.

// agentKind values mirror the design's `kind:` field.
const (
	agentKindHosted = "hosted"
	agentKindPrompt = "prompt"
)

// foundryServiceConfig is the typed view of a single `host:
// microsoft.foundry` service entry's Foundry-scoped state. Field shapes
// mirror the unified-design YAML keys; nextstep consumes only the subset it
// needs (names, kinds, protocols, env, endpoint, and connection/deployment
// identifiers).
type foundryServiceConfig struct {
	// Endpoint, when non-empty, points at an EXISTING Foundry project
	// (provision is skipped). Its presence is a "project already exists"
	// signal for the next-step resolver.
	Endpoint    string
	Deployments []foundryDeployment
	Connections []foundryConnection
	Toolboxes   []foundryToolbox
	Skills      []foundrySkill
	Routines    []foundryRoutine
	Agents      []foundryAgent
}

// foundryDeployment is one model deployment declared at the service level.
type foundryDeployment struct {
	Name  string             `json:"name,omitempty"`
	Model foundryDeployModel `json:"model"`
}

// foundryDeployModel is the `model:` block of a deployment.
type foundryDeployModel struct {
	Name    string `json:"name,omitempty"`
	Format  string `json:"format,omitempty"`
	Version string `json:"version,omitempty"`
}

// foundryConnection is one project connection. nextstep only needs the
// name plus category/target for doctor remediation detail strings.
type foundryConnection struct {
	Name     string `json:"name,omitempty"`
	Category string `json:"category,omitempty"`
	Target   string `json:"target,omitempty"`
}

// foundryToolbox is one toolbox. nextstep only needs the name (the
// per-toolbox MCP endpoint env var is derived from it by envkey).
type foundryToolbox struct {
	Name string `json:"name,omitempty"`
}

// foundrySkill is one skill. Only the name is consumed today.
type foundrySkill struct {
	Name string `json:"name,omitempty"`
}

// foundryRoutine is one scheduled routine. Only the name is consumed today.
type foundryRoutine struct {
	Name string `json:"name,omitempty"`
}

// foundryAgent is one agent nested under the service. `kind: hosted`
// agents carry code/runtime fields (project, startupCommand, protocols);
// `kind: prompt` agents are pure config. Env values may contain ${VAR}
// (azd client-side) or ${{...}} (Foundry server-side) references, which
// arrive verbatim (core applies no substitution to AdditionalProperties).
type foundryAgent struct {
	Name           string            `json:"name,omitempty"`
	Kind           string            `json:"kind,omitempty"`
	Description    string            `json:"description,omitempty"`
	Project        string            `json:"project,omitempty"`
	StartupCommand string            `json:"startupCommand,omitempty"`
	Protocols      []foundryProtocol `json:"protocols,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
}

// foundryProtocol is one `protocols[]` entry under an agent.
type foundryProtocol struct {
	Protocol string `json:"protocol,omitempty"`
	Version  string `json:"version,omitempty"`
}

// foundryServiceRaw is the first-stage decode of the service properties.
// Every `oneOf` array (per the spec, each item may be an inline object or a
// `$ref` file include) is kept as raw maps so $ref resolution + overlay can
// happen at the map level before typed conversion.
type foundryServiceRaw struct {
	Endpoint    string           `json:"endpoint,omitempty"`
	Deployments []map[string]any `json:"deployments,omitempty"`
	Connections []map[string]any `json:"connections,omitempty"`
	Toolboxes   []map[string]any `json:"toolboxes,omitempty"`
	Skills      []map[string]any `json:"skills,omitempty"`
	Routines    []map[string]any `json:"routines,omitempty"`
	Agents      []map[string]any `json:"agents,omitempty"`
}

// decodeFoundryService converts a service's AdditionalProperties
// (structpb.Struct, as forwarded by core azd over gRPC) into the typed
// foundryServiceConfig, resolving any `$ref:` file includes relative to
// projectPath.
//
// Best-effort by contract: a nil struct yields an empty config; a decode
// error is returned so callers can surface it in --debug logs, but the
// (possibly partial) config is still returned. `$ref` resolution failures
// are silent — an unresolved item is dropped (its empty Name causes every
// downstream consumer to skip it).
func decodeFoundryService(props *structpb.Struct, projectPath string) (foundryServiceConfig, error) {
	var cfg foundryServiceConfig
	if props == nil {
		return cfg, nil
	}

	raw, err := protojson.Marshal(props)
	if err != nil {
		return cfg, fmt.Errorf("marshal service properties: %w", err)
	}
	var rawCfg foundryServiceRaw
	if err := json.Unmarshal(raw, &rawCfg); err != nil {
		return cfg, fmt.Errorf("decode foundry service config: %w", err)
	}

	cfg.Endpoint = rawCfg.Endpoint
	cfg.Deployments = decodeFoundryItems[foundryDeployment](rawCfg.Deployments, projectPath)
	cfg.Connections = decodeFoundryItems[foundryConnection](rawCfg.Connections, projectPath)
	cfg.Toolboxes = decodeFoundryItems[foundryToolbox](rawCfg.Toolboxes, projectPath)
	cfg.Skills = decodeFoundryItems[foundrySkill](rawCfg.Skills, projectPath)
	cfg.Routines = decodeFoundryItems[foundryRoutine](rawCfg.Routines, projectPath)
	cfg.Agents = decodeFoundryItems[foundryAgent](rawCfg.Agents, projectPath)
	return cfg, nil
}

// decodeFoundryItems resolves each raw array item ($ref + overlay) and
// converts it to the typed element T. Items that fail typed conversion are
// skipped. Returns nil for an empty input so callers see the same shape as
// an absent key.
func decodeFoundryItems[T any](items []map[string]any, projectPath string) []T {
	if len(items) == 0 {
		return nil
	}
	out := make([]T, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		merged, ok := resolveFoundryItem(item, projectPath)
		if !ok {
			continue
		}
		var typed T
		if mapToTyped(merged, &typed) {
			out = append(out, typed)
		}
	}
	return out
}

// resolveFoundryItem applies the spec's `$ref` rules to one array item:
//   - Inline item (no `$ref`): returned unchanged.
//   - `$ref` item: the referenced sibling YAML file is loaded; its relative
//     `project:` path is rebased to the referenced file's own directory
//     (per the spec, split-file paths are relative to that file, not
//     azure.yaml); then any sibling keys next to `$ref` overlay (shallow,
//     top-level) on top of the loaded map. A `$ref` that cannot be read is
//     dropped (ok=false).
func resolveFoundryItem(item map[string]any, projectPath string) (map[string]any, bool) {
	ref, hasRef := item["$ref"].(string)
	if !hasRef || strings.TrimSpace(ref) == "" {
		return item, true
	}

	loaded, ok := loadRefMap(projectPath, ref)
	if !ok {
		return nil, false
	}
	rebaseRefProject(loaded, path.Dir(filepathToSlash(ref)))

	// Shallow top-level overlay: sibling keys (except $ref) replace the
	// loaded value. Scalars and arrays replace wholesale, matching the
	// spec's "easy to predict" rule.
	for k, v := range item {
		if k == "$ref" {
			continue
		}
		loaded[k] = v
	}
	return loaded, true
}

// loadRefMap reads the YAML file at refPath (resolved path-safely under
// projectPath) into a generic map. All failure modes (path escape, missing
// file, malformed YAML) return ok=false.
func loadRefMap(projectPath, refPath string) (map[string]any, bool) {
	resolved, err := paths.JoinAllowRoot(projectPath, refPath)
	if err != nil {
		return nil, false
	}
	data, err := os.ReadFile(resolved) //nolint:gosec // path is validated under the project root
	if err != nil || len(data) == 0 {
		return nil, false
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}

// rebaseRefProject rewrites a loaded item's relative `project:` path so it
// is relative to the project root, given the directory that holds the
// referenced file. Absolute paths and non-string values are left alone.
// nextstep only consumes `project` (for README discovery); `instructions`
// and nested `$ref` rebasing are intentionally out of scope here.
func rebaseRefProject(m map[string]any, baseDir string) {
	if baseDir == "" || baseDir == "." {
		return
	}
	p, ok := m["project"].(string)
	if !ok || p == "" {
		return
	}
	slashed := filepathToSlash(p)
	if path.IsAbs(slashed) {
		return
	}
	m["project"] = path.Clean(baseDir + "/" + slashed)
}

// mapToTyped converts a generic map into the typed element via a JSON
// round-trip (the element structs carry json tags matching the YAML keys).
// Returns false on any marshal/unmarshal error so the caller can skip a
// malformed entry rather than surface a zero value as if it were real.
func mapToTyped[T any](m map[string]any, out *T) bool {
	b, err := json.Marshal(m)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, out) == nil
}

// filepathToSlash normalizes OS path separators to forward slashes so the
// slash-based path package operates correctly on Windows-authored refs.
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, "\\", "/")
}
