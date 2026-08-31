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

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// promptNodeKind enumerates the resolvable dependency kinds in a prompt-agent
// deploy graph. Additional kinds (file_store, skill, toolbox, connection, rbac,
// deployment) are registered by later stages of the deploy engine.
type promptNodeKind string

const (
	nodeAgent       promptNodeKind = "agent"
	nodeDeployment  promptNodeKind = "deployment"
	nodeConnection  promptNodeKind = "connection"
	nodeRBAC        promptNodeKind = "rbac"
	nodeMemoryStore promptNodeKind = "memory_store"
	nodeSkill       promptNodeKind = "skill"
	nodeToolbox     promptNodeKind = "toolbox"
	nodePolicy      promptNodeKind = "policy"
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
	// agentDir is the folder holding agent.yaml plus its skills/ folder.
	agentDir string

	// managed is the parsed agent definition. Nodes may enrich managed.Tools
	// with resolved bindings before publish.
	managed *agent_yaml.PromptAgent

	// settings holds the resolved harness/connection target for the agent.
	settings *PromptAgentSettings

	// env is a snapshot of azd environment values used to resolve targets.
	env        map[string]string
	credential azcore.TokenCredential

	// bindings holds symbolic outputs produced by resolved nodes (for example
	// "toolbox_mcp_url") that later nodes read.
	bindings map[string]any

	// warn reports a non-fatal finding to the user. It is set for the duration
	// of resolve and is nil otherwise, so nodes must go through warnf.
	//
	// A dedicated channel exists because the extension's stderr is not forwarded
	// to the azd console: anything not routed through the progress reporter is
	// invisible during a deploy.
	warn func(string)

	// nodes is the ordered set of dependencies to validate and resolve.
	nodes []promptNode
}

func (g *promptGraph) projectEndpoint() string {
	if g.settings != nil && strings.TrimSpace(g.settings.ProjectEndpoint) != "" {
		return g.settings.ProjectEndpoint
	}
	return g.env["FOUNDRY_PROJECT_ENDPOINT"]
}

// warnf reports a non-fatal finding discovered while resolving the graph.
// No-ops when the graph is not being resolved through resolve (e.g. in tests).
func (g *promptGraph) warnf(format string, args ...any) {
	if g.warn == nil {
		return
	}
	g.warn(fmt.Sprintf(format, args...))
}

// pluralize appends "s" to noun when count is not 1, so warning text reads
// naturally for both a single finding and several.
func pluralize(noun string, count int) string {
	if count == 1 {
		return noun
	}
	return noun + "s"
}

// newPromptGraph builds a graph for the given agent. Only the agent node is
// registered today; file/skill/connection nodes are added by later stages.
func newPromptGraph(
	agentDir string,
	managed *agent_yaml.PromptAgent,
	settings *PromptAgentSettings,
	env map[string]string,
	credential azcore.TokenCredential,
) (*promptGraph, error) {
	g := &promptGraph{
		agentDir:   agentDir,
		managed:    managed,
		settings:   settings,
		env:        env,
		credential: credential,
		bindings:   map[string]any{},
	}

	// The model deployment is resolved first: create-if-missing so the harness
	// has a model to bind to before the agent version is published.
	if node := deploymentNode(g, func() (deploymentResolver, error) {
		return provisionedDeploymentResolver{}, nil
	}); node != nil {
		g.nodes = append(g.nodes, *node)
	}

	// A declared memory: block provisions a memory store and contributes the
	// memory_search_preview tool that reads from it.
	if node := memoryNode(g, managed.Memory, func() (memoryStoreEnsurer, error) {
		return newFoundryMemoryStoreEnsurer(settings, credential)
	}); node != nil {
		g.nodes = append(g.nodes, *node)
	}

	// Convention: a non-empty skills/ folder contributes the agent's skills.
	// The bundles themselves are created and versioned by the sibling
	// `host: azure.ai.skill` services that `azd ai agent init` emits; these
	// nodes only attach the versions those services published. How they are
	// reached splits on the harness — a managed agent provisions them into its
	// sandbox by pinning them on the harness block, while a plain prompt agent
	// references them by name and runs them with a shell tool.
	skills, err := scanSkillsDir(agentDir)
	if err != nil {
		return nil, err
	}
	if managed.HarnessType() != "" {
		// An explicit toolbox: reference is a separate feature from skills: it
		// attaches an existing shared toolbox as an mcp tool. Skills are never
		// routed through a toolbox of azd's making — the harness already has a
		// service-owned system toolbox whose name, version and lifecycle the
		// customer does not manage.
		if node := toolboxNode(g, managed.Toolbox, func() (toolboxBuilder, error) {
			return newFoundryToolboxBuilder(settings)
		}); node != nil {
			g.nodes = append(g.nodes, *node)
		}
		if node := skillsHarnessNode(g, skills); node != nil {
			g.nodes = append(g.nodes, *node)
		}
	} else {
		if node := skillsShellNode(g, skills, managed.Toolbox); node != nil {
			g.nodes = append(g.nodes, *node)
		}
	}

	// Connections are provisioned by sibling azure.ai.connection services. This
	// node only verifies their deployment markers before publishing the agent.
	if node := connectionsNode(g); node != nil {
		g.nodes = append(g.nodes, *node)
	}

	// Guardrails are checked just before the agent node so a policy that does
	// not exist is reported by name instead of as an opaque service rejection
	// from the create call, and is replaced with the account's built-in default
	// rather than failing the deploy.
	if node := policiesNode(g, func() (raiPolicyLister, error) {
		return azureRaiPolicyLister(credential)
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
			if err := agent_yaml.ValidateAgentName(g.managed.Name); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					fmt.Sprintf("prompt agent name %q is invalid: %s", g.managed.Name, err),
					"set a valid prompt agent name",
				)
			}
			if strings.TrimSpace(g.managed.Model) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"prompt agent requires a non-empty model",
					"set 'model' in agent.yaml to the name of a deployment "+
						"declared under your azure.ai.project service (e.g. model: gpt-4.1-mini)",
				)
			}
			if strings.TrimSpace(g.managed.Instructions) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"prompt agent requires non-empty instructions",
					"set 'instructions' in agent.yaml",
				)
			}
			if err := g.managed.ValidateHarnessFeatures(); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					err.Error(),
					"remove that configuration from agent.yaml, or drop 'harness:' to run as a "+
						"plain prompt agent, which supports it",
				)
			}
			// A tool the service cannot identify is dropped silently, producing an
			// agent that is missing a capability its manifest claims. Catch the
			// unambiguous cases before anything is provisioned.
			if err := g.managed.ValidateTools(); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					err.Error(),
					"each entry under 'tools:' must be a mapping with a string 'type', "+
						"for example '- type: file_search'",
				)
			}
			// A harness owns sampling, response format and tool dispatch. The
			// service rejects a manifest that sets them rather than ignoring it,
			// so name the offending key before anything is provisioned.
			if err := g.managed.ValidateHarnessFields(); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					err.Error(),
					"remove that field from agent.yaml, or drop 'harness:' to run as a "+
						"plain prompt agent, which accepts it",
				)
			}
			if err := g.managed.ValidateHarnessTools(); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					err.Error(),
					"remove that tool from agent.yaml, or drop 'harness:' to run as a "+
						"plain prompt agent, which supports it",
				)
			}
			// A bare RAI policy name reaches the service as "invalid or does not
			// exist", which reads like a missing policy rather than a malformed
			// value. Catch the shape here so the message points at the right fix.
			if err := g.managed.ValidatePolicies(); err != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					err.Error(),
					"list the policy IDs on your account with: az rest --method get --url "+
						"\"https://management.azure.com/subscriptions/<sub>/resourceGroups/<rg>/"+
						"providers/Microsoft.CognitiveServices/accounts/<account>/"+
						"raiPolicies?api-version=2024-10-01\" --query \"value[].id\" -o tsv",
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
	if progress != nil {
		g.warn = func(message string) { progress("Warning: " + message) }
		defer func() { g.warn = nil }()
	}

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

	// Reported after validation and before any node injects its own tools, so
	// the list only ever names types the author actually wrote.
	if unrecognized := g.managed.UnrecognizedToolTypes(); len(unrecognized) > 0 {
		g.warnf(
			"agent.yaml declares unrecognized tool %s: %s. "+
				"These are sent as authored, but a type the service does not recognize is ignored "+
				"without error \u2014 check the spelling if the capability does not appear.",
			pluralize("type", len(unrecognized)),
			strings.Join(unrecognized, ", "),
		)
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
// The resolved bindings are returned so the caller can persist ids (such as the
// vector store id) that must survive into the next deploy.
func (p *AgentServiceTargetProvider) resolvePromptAgentGraph(
	ctx context.Context,
	managed *agent_yaml.PromptAgent,
	settings *PromptAgentSettings,
	env map[string]string,
	progress azdext.ProgressReporter,
) (map[string]any, error) {
	// The skills/ convention folder sits next to the file
	// that supplies the definition. With the definition inline on the service
	// entry there is no such file, so they are anchored at the service
	// directory instead — the same place `azd ai agent init` scaffolds them.
	agentDir := p.servicePath
	if p.agentDefinitionPath != "" {
		agentDir = filepath.Dir(p.agentDefinitionPath)
	}
	g, err := newPromptGraph(agentDir, managed, settings, env, p.credential)
	if err != nil {
		return nil, err
	}
	if err := g.resolve(ctx, progress); err != nil {
		return nil, err
	}
	return g.bindings, nil
}
