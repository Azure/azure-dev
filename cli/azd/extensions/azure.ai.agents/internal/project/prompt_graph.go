// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// promptNodeKind enumerates the resolvable dependency kinds in a prompt-agent
// deploy graph. Additional kinds (file_store, skill, toolbox, connection, rbac,
// deployment) are registered by later stages of the deploy engine.
type promptNodeKind string

const (
	nodeAgent      promptNodeKind = "agent"
	nodeDeployment promptNodeKind = "deployment"
	nodeConnection promptNodeKind = "connection"
	nodeRBAC       promptNodeKind = "rbac"
	nodeFileStore  promptNodeKind = "file_store"
	nodeSkill      promptNodeKind = "skill"
	nodeToolbox    promptNodeKind = "toolbox"
)

// promptNode is a single dependency in the prompt-agent deploy graph. Validate
// is pure and runs for every node before any Resolve executes, so a graph is
// fully validated before the first live mutation. Resolve is idempotent and
// create-if-missing; it writes any outputs later nodes consume into
// promptGraph.bindings.
type promptNode struct {
	Kind     promptNodeKind
	ID       string
	Validate func() error
	Resolve  func(ctx context.Context) error
}

// promptGraph is the internal, non-user-facing dependency graph for one
// prompt-agent deploy. It is derived from the agent folder plus agent.yaml,
// validated as a whole, then resolved in registration (dependency) order. None
// of this machinery is exposed in the YAML.
type promptGraph struct {
	// agentDir is the folder holding agent.yaml plus any convention folders
	// (instructions.md, files/, skills/).
	agentDir string

	// managed is the parsed agent definition. Nodes may enrich managed.Tools
	// with resolved bindings (e.g. a file_search or mcp tool) before publish.
	managed *agent_yaml.PromptAgent

	// settings holds the resolved harness/connection target for the agent.
	settings *PromptAgentSettings

	// env is a snapshot of azd environment values used to resolve targets.
	env map[string]string

	// bindings holds symbolic outputs produced by resolved nodes (for example
	// "vector_store_id" or "toolbox_mcp_url") that later nodes read.
	bindings map[string]any

	// nodes is the ordered set of dependencies to validate and resolve.
	nodes []promptNode
}

// newPromptGraph builds a graph for the given agent. Only the agent node is
// registered today; file/skill/connection nodes are added by later stages.
func newPromptGraph(
	agentDir string,
	managed *agent_yaml.PromptAgent,
	settings *PromptAgentSettings,
	env map[string]string,
) (*promptGraph, error) {
	g := &promptGraph{
		agentDir: agentDir,
		managed:  managed,
		settings: settings,
		env:      env,
		bindings: map[string]any{},
	}

	// The model deployment is resolved first: create-if-missing so the harness
	// has a model to bind to before the agent version is published.
	if node := deploymentNode(g, func() (deploymentResolver, error) {
		return provisionedDeploymentResolver{}, nil
	}); node != nil {
		g.nodes = append(g.nodes, *node)
	}

	// Convention: a non-empty files/ folder contributes a file_search tool
	// backed by an uploaded vector store.
	files, err := scanFilesDir(agentDir)
	if err != nil {
		return nil, err
	}
	if node := fileStoreNode(g, files, func() (vectorStoreBuilder, error) {
		return newFoundryVectorStoreBuilder(settings)
	}); node != nil {
		g.nodes = append(g.nodes, *node)
	}

	// Convention: a non-empty skills/ folder (or an explicit toolbox reference)
	// contributes an mcp tool backed by a Foundry toolbox version.
	skills, err := scanSkillsDir(agentDir)
	if err != nil {
		return nil, err
	}
	if node := toolboxNode(g, skills, managed.Toolbox, func() (toolboxBuilder, error) {
		return newFoundryToolboxBuilder(settings)
	}); node != nil {
		g.nodes = append(g.nodes, *node)
	}

	// Declared connections are resolved last among the feature stages: existing
	// connections are used as-is, missing ones are created (Entra default), and
	// each referenced tool's required role is assigned.
	if node := connectionsNode(g, func() (connectionResolver, error) {
		return newFoundryConnectionResolver(settings)
	}); node != nil {
		g.nodes = append(g.nodes, *node)
	}

	// The agent node is terminal and validated last.
	g.nodes = append(g.nodes, g.agentNode())
	return g, nil
}

// agentNode is the terminal node representing the published agent version. Its
// validation enforces the minimum contract (model + instructions) up front so
// the deploy fails before any dependency is resolved when the definition is
// incomplete.
func (g *promptGraph) agentNode() promptNode {
	return promptNode{
		Kind: nodeAgent,
		ID:   g.managed.Name,
		Validate: func() error {
			if strings.TrimSpace(g.managed.Model) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"prompt agent requires a non-empty model",
					"set 'model' in agent.yaml (e.g. model: gpt-4.1-mini)",
				)
			}
			if strings.TrimSpace(g.managed.Instructions) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"prompt agent requires non-empty instructions",
					"set 'instructions' in agent.yaml or add a sibling instructions.md",
				)
			}
			return nil
		},
		Resolve: func(ctx context.Context) error { return nil },
	}
}

// resolve validates the entire graph, then resolves each node in registration
// order. Validation runs to completion before any Resolve so a failure never
// leaves a half-wired agent.
func (g *promptGraph) resolve(ctx context.Context, progress azdext.ProgressReporter) error {
	// Surface which convention nodes were discovered via the progress reporter
	// (the extension's stderr is not forwarded to the azd console, so this is
	// the only reliable way to report it during a deploy).
	if progress != nil {
		kinds := make([]string, 0, len(g.nodes))
		for _, n := range g.nodes {
			kinds = append(kinds, string(n.Kind))
		}
		progress(fmt.Sprintf("Prompt graph nodes: %s", strings.Join(kinds, ", ")))
	}

	for _, n := range g.nodes {
		if n.Validate == nil {
			continue
		}
		if err := n.Validate(); err != nil {
			return err
		}
	}

	for _, n := range g.nodes {
		if n.Resolve == nil {
			continue
		}
		if progress != nil {
			progress(fmt.Sprintf("Resolving %s", n.Kind))
		}
		if err := n.Resolve(ctx); err != nil {
			return err
		}
	}

	return nil
}

// resolvePromptAgentGraph builds and resolves the deploy graph for a prompt
// agent. It is called by deployPromptAgent before the create request is built,
// so any resolved bindings are reflected in the published agent definition.
func (p *AgentServiceTargetProvider) resolvePromptAgentGraph(
	ctx context.Context,
	managed *agent_yaml.PromptAgent,
	settings *PromptAgentSettings,
	env map[string]string,
	progress azdext.ProgressReporter,
) error {
	agentDir := ""
	if p.agentDefinitionPath != "" {
		agentDir = filepath.Dir(p.agentDefinitionPath)
	}
	g, err := newPromptGraph(agentDir, managed, settings, env)
	if err != nil {
		return err
	}
	return g.resolve(ctx, progress)
}
