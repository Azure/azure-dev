// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/braydonk/yaml"
)

// promptSkillsDirName is the conventional folder whose subfolders are Agent-Skills
// bundles registered into a toolbox and attached via an mcp tool.
const promptSkillsDirName = "skills"

// skillFileName is the required manifest inside each skill bundle.
const skillFileName = "SKILL.md"

// toolboxMcpURLBindingKey is the graph binding under which the resolved toolbox
// MCP url is published for later nodes / observability.
const toolboxMcpURLBindingKey = "toolbox_mcp_url"

// skillMeta is the parsed SKILL.md content: the required frontmatter fields plus
// the Markdown body that becomes the skill's injected instructions. Version is
// optional (the service assigns one); when set via metadata.version it pins the
// toolbox skill reference to that immutable snapshot.
type skillMeta struct {
	Name         string
	Description  string
	Version      string
	Instructions string
}

// skillBundle is one skills/<name>/ directory with its parsed metadata.
type skillBundle struct {
	// Dir is the subfolder name (used as the skill/toolbox label).
	Dir string
	// Path is the absolute path to the bundle directory.
	Path string
	// Meta is the parsed SKILL.md frontmatter.
	Meta skillMeta
}

// toolboxRef identifies an existing toolbox to attach by reference.
type toolboxRef struct {
	Name       string
	Version    string
	Connection string
	// MCPEndpoint is the toolbox's MCP url as published by its sibling
	// `host: azure.ai.toolbox` service, when that service deployed in this
	// environment. It is authoritative: the toolboxes extension owns the
	// toolbox's lifecycle and knows the endpoint it actually created, whereas
	// azd can only guess one from the name and version. Empty for a toolbox
	// that has no sibling service, e.g. one created outside of azure.yaml.
	MCPEndpoint string
}

// toolboxAttachment is the result of registering or resolving a toolbox: the
// MCP url the agent connects to plus the name of the project connection that
// authenticates the agent to that endpoint. The connection name is what the
// injected mcp tool carries as its project_connection_id — without it the agent
// has no credential to reach the toolbox and its skills are never invoked.
type toolboxAttachment struct {
	McpURL         string
	ConnectionName string
}

// toolboxBuilder resolves an existing toolbox named by an explicit `toolbox:`
// reference, returning the toolbox MCP url and the project connection that
// fronts it. The seam keeps the graph node unit-testable without a live
// endpoint.
//
// There is deliberately no "create a toolbox" operation here. Every harnessed
// agent already has a system toolbox that the service creates, versions and
// deletes with the agent, and whose name customers never supply.
type toolboxBuilder interface {
	// ResolveToolbox returns the MCP url and backing project connection of an
	// existing toolbox version.
	ResolveToolbox(ctx context.Context, ref toolboxRef) (toolboxAttachment, error)
}

// SkillBundleRef is the identity `azd ai agent init` needs to emit one
// `host: azure.ai.skill` sibling service per skills/<dir>/ folder: the folder to
// point the service's archive: at, and the name and description its SKILL.md
// declares.
type SkillBundleRef struct {
	// Name is the skill name from SKILL.md frontmatter, defaulting to the
	// folder name. It becomes the azure.yaml service key, which the skills
	// extension uses as the skill name.
	Name string
	// Description is the skill description from SKILL.md frontmatter.
	Description string
	// RelPath is the bundle folder relative to the agent directory, in
	// forward-slash form (e.g. "skills/code-review"), ready to be joined onto
	// the service path and written as archive:.
	RelPath string
}

// ScanSkillBundles returns one SkillBundleRef per skills/<name>/ folder under
// agentDir, sorted by folder name. A missing or empty folder returns (nil, nil).
//
// It exists so the init command can emit the sibling skill services without
// reaching into the deploy engine's internal bundle representation.
func ScanSkillBundles(agentDir string) ([]SkillBundleRef, error) {
	bundles, err := scanSkillsDir(agentDir)
	if err != nil {
		return nil, err
	}
	refs := make([]SkillBundleRef, 0, len(bundles))
	for _, b := range bundles {
		name := strings.TrimSpace(b.Meta.Name)
		if name == "" {
			name = b.Dir
		}
		refs = append(refs, SkillBundleRef{
			Name:        name,
			Description: b.Meta.Description,
			RelPath:     promptSkillsDirName + "/" + b.Dir,
		})
	}
	return refs, nil
}

// scanSkillsDir returns the skill bundles under <agentDir>/skills, one per
// subfolder, sorted by name. Each bundle's SKILL.md is parsed. A missing or
// empty folder returns (nil, nil).
func scanSkillsDir(agentDir string) ([]skillBundle, error) {
	if strings.TrimSpace(agentDir) == "" {
		return nil, nil
	}
	dir := filepath.Join(agentDir, promptSkillsDirName)

	f, err := os.Open(dir) //nolint:gosec // agentDir derives from the resolved agent.yaml path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening skills directory %q: %w", dir, err)
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("reading skills directory %q: %w", dir, err)
	}

	var bundles []skillBundle
	for _, name := range names {
		if strings.HasPrefix(name, ".") {
			continue
		}
		bundleDir := filepath.Join(dir, name)
		// Lstat, not Stat: a symlinked bundle would let a cloned agent project
		// package and upload files from anywhere on the developer's machine.
		info, statErr := os.Lstat(bundleDir)
		if statErr != nil {
			return nil, fmt.Errorf("stat %q: %w", bundleDir, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("skill bundle %q is a symbolic link", name),
				"replace the link with the skill folder itself; symlinks are not packaged",
			)
		}
		if !info.IsDir() {
			continue
		}
		meta, parseErr := parseSkillMD(filepath.Join(bundleDir, skillFileName))
		if parseErr != nil {
			return nil, parseErr
		}
		if strings.TrimSpace(meta.Name) == "" {
			meta.Name = name
		}
		bundles = append(bundles, skillBundle{Dir: name, Path: bundleDir, Meta: meta})
	}

	slices.SortFunc(bundles, func(a, b skillBundle) int {
		return strings.Compare(a.Dir, b.Dir)
	})
	return bundles, nil
}

// parseSkillMD parses the frontmatter of a SKILL.md file. The frontmatter is a
// YAML block delimited by leading and trailing `---` lines. name, description,
// and metadata.version are required.
func parseSkillMD(path string) (skillMeta, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path derived from the agent's skills/ folder
	if err != nil {
		return skillMeta{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("failed to read %s: %s", skillFileName, err),
			"ensure each skills/<name>/ folder contains a SKILL.md file",
		)
	}

	front, err := extractFrontmatter(string(data))
	if err != nil {
		return skillMeta{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("%s at %q: %s", skillFileName, path, err),
			"add a YAML frontmatter block delimited by --- at the top of SKILL.md",
		)
	}

	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Metadata    struct {
			Version string `yaml:"version"`
		} `yaml:"metadata"`
	}
	if err := yaml.Unmarshal([]byte(front.frontmatter), &fm); err != nil {
		return skillMeta{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("%s frontmatter at %q is not valid YAML: %s", skillFileName, path, err),
			"fix the SKILL.md frontmatter",
		)
	}

	meta := skillMeta{
		Name:         fm.Name,
		Description:  fm.Description,
		Version:      fm.Metadata.Version,
		Instructions: front.body,
	}
	if strings.TrimSpace(meta.Description) == "" {
		return skillMeta{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("%s at %q is missing 'description'", skillFileName, path),
			"add a description to the SKILL.md frontmatter",
		)
	}
	// Version is optional: the Skills API assigns a version when omitted. When
	// present (metadata.version) it pins the toolbox reference to that snapshot.
	return meta, nil
}

// frontmatterResult holds the split of a SKILL.md into its YAML frontmatter and
// the Markdown body that follows it.
type frontmatterResult struct {
	frontmatter string
	body        string
}

// extractFrontmatter splits SKILL.md into the YAML block between the first two
// `---` lines and the Markdown body after it.
func extractFrontmatter(content string) (frontmatterResult, error) {
	trimmed := strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return frontmatterResult{}, fmt.Errorf("missing frontmatter delimiter")
	}
	// Drop the opening delimiter line.
	rest := trimmed[len("---"):]
	rest = strings.TrimLeft(rest, "\r\n")
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return frontmatterResult{}, fmt.Errorf("unterminated frontmatter block")
	}
	front := before
	// The body starts after the closing `---` line. Consume only the remainder of
	// that fence line (extra dashes from a longer `-----` fence plus trailing
	// whitespace) and stop at its newline. A cut set mixing "-" with newlines
	// would cross into the body and strip the leading dash from a `- bullet` or
	// a `---` break on the body's first line.
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		after = after[nl+1:]
	} else {
		after = ""
	}
	return frontmatterResult{frontmatter: front, body: after}, nil
}

// injectMcpTool ensures the agent's tools include an mcp tool for the given
// toolbox label and MCP url. An existing mcp tool with the same server_url is
// left in place (not duplicated). When connectionName is non-empty it is set as
// the tool's project_connection_id so the agent can authenticate to the toolbox
// MCP endpoint; without it the toolbox skills are never invoked. The managed
// definition is mutated in place.
func injectMcpTool(managed *agent_yaml.PromptAgent, serverLabel, mcpURL, connectionName string) {
	if managed == nil || strings.TrimSpace(mcpURL) == "" {
		return
	}
	for _, raw := range managed.Tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", tool["type"]) != "mcp" {
			continue
		}
		if fmt.Sprintf("%v", tool["server_url"]) == mcpURL {
			// Already present — backfill the connection id if it was missing so
			// a previously connection-less mcp tool starts authenticating.
			if strings.TrimSpace(connectionName) != "" {
				if _, has := tool["project_connection_id"]; !has {
					tool["project_connection_id"] = connectionName
				}
			}
			return
		}
	}
	mcpTool := map[string]any{
		"type":             "mcp",
		"server_label":     serverLabel,
		"server_url":       mcpURL,
		"require_approval": "always",
	}
	if strings.TrimSpace(connectionName) != "" {
		mcpTool["project_connection_id"] = connectionName
	}
	managed.Tools = append(managed.Tools, mcpTool)
}

// skillsShellNode builds the skills graph node for a *harness-less* prompt
// agent: bundles are referenced by name and version on the definition.
//
// This is the counterpart to toolboxNode, which serves managed agents. The two
// are mutually exclusive — a toolbox is only reachable from inside a harness
// sandbox, and a shell tool is rejected by a harness — so the caller picks one
// based on whether a harness is named.
func skillsShellNode(
	g *promptGraph,
	skills []skillBundle,
	ref *agent_yaml.ToolboxReference,
) *promptNode {
	if len(skills) == 0 && ref == nil {
		return nil
	}
	return &promptNode{
		Kind: nodeSkill,
		ID:   promptSkillsDirName,
		Validate: func() error {
			// A toolbox is provisioned and reached through the harness sandbox.
			// Without a harness there is nothing to reach it from, so accepting
			// the reference would deploy an agent whose skills never run.
			if ref != nil {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"toolbox: is only available to an agent that names a harness",
					"add a 'harness:' block with type "+agent_api.ManagedAgentHarnessGitHubCopilot+
						" to agent.yaml, or remove 'toolbox:' and put the "+
						"skills in a skills/ folder next to agent.yaml",
				)
			}
			return validateSkillBundleInstructions(skills)
		},
		Resolve: func(_ context.Context) error {
			resolved, err := resolveSkillMarkers(skills, g.projectEndpoint(), g.env)
			if err != nil {
				return err
			}
			for _, s := range resolved {
				addResolvedPromptSkill(g.managed, s.Name, s.Version)
			}
			return nil
		},
	}
}

// skillsHarnessNode builds the skills graph node for a *harnessed* prompt
// agent: bundles are pinned onto the harness, which provisions them into the
// sandbox that starts up to run the agent.
//
// This is the counterpart to skillsShellNode, which serves harness-less agents.
// Nothing is attached as a tool here — a skill is not a tool, and the harness
// loads its pinned skills when the environment starts.
func skillsHarnessNode(
	g *promptGraph,
	skills []skillBundle,
) *promptNode {
	if len(skills) == 0 {
		return nil
	}
	return &promptNode{
		Kind:     nodeSkill,
		ID:       promptSkillsDirName,
		Validate: func() error { return validateSkillBundleInstructions(skills) },
		Resolve: func(_ context.Context) error {
			resolved, err := resolveSkillMarkers(skills, g.projectEndpoint(), g.env)
			if err != nil {
				return err
			}
			for _, s := range resolved {
				addResolvedPromptSkill(g.managed, s.Name, s.Version)
			}
			return nil
		},
	}
}

func addResolvedPromptSkill(agent *agent_yaml.PromptAgent, name, version string) {
	for i, skill := range agent.ResolvedSkills {
		if strings.EqualFold(skill.Name, name) {
			if skill.Version == "" {
				agent.ResolvedSkills[i].Version = version
			}
			return
		}
	}
	agent.ResolvedSkills = append(agent.ResolvedSkills, agent_yaml.HarnessSkillRef{Name: name, Version: version})
}

// validateSkillBundleInstructions rejects a bundle whose SKILL.md has no body.
// The skills extension uploads the folder as-is, so an empty body would publish
// a skill version that instructs the agent to do nothing.
func validateSkillBundleInstructions(skills []skillBundle) error {
	for _, s := range skills {
		if strings.TrimSpace(s.Meta.Instructions) == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf("skill %q has no instructions (empty SKILL.md body)", s.Dir),
				"add Markdown content below the frontmatter in the skill's SKILL.md",
			)
		}
	}
	return nil
}

// toolboxNode attaches an existing shared toolbox by reference, as an mcp tool.
//
// It is reachable only from an explicit `toolbox:` block in agent.yaml. Skills
// no longer travel this path: every harnessed agent already gets a system
// toolbox whose name, version, endpoint and lifecycle the service owns, so azd
// creating a second toolbox of its own to carry skills both duplicated that and
// left the skills invisible as skills.
func toolboxNode(
	g *promptGraph,
	ref *agent_yaml.ToolboxReference,
	newBuilder func() (toolboxBuilder, error),
) *promptNode {
	if ref == nil {
		return nil
	}
	return &promptNode{
		Kind: nodeToolbox,
		ID:   ref.Name,
		Validate: func() error {
			if strings.TrimSpace(ref.Name) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"toolbox reference is missing a name",
					"set toolbox.name in agent.yaml",
				)
			}
			if strings.TrimSpace(ref.Connection) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					fmt.Sprintf("toolbox %q is missing its connection reference", ref.Name),
					"set toolbox.connection to the sibling azure.ai.connection service name",
				)
			}
			return nil
		},
		Resolve: func(ctx context.Context) error {
			builder, err := newBuilder()
			if err != nil {
				return err
			}
			// Prefer the endpoint the sibling azure.ai.toolbox service published
			// over one synthesized from the name, so the two extensions cannot
			// disagree about where the toolbox lives.
			mcpEndpoint, err := siblingToolboxEndpoint(ref.Name, g.projectEndpoint(), g.env)
			if err != nil {
				return err
			}
			attachment, err := builder.ResolveToolbox(ctx, toolboxRef{
				Name:        ref.Name,
				Version:     ref.Version,
				Connection:  ref.Connection,
				MCPEndpoint: mcpEndpoint,
			})
			if err != nil {
				return err
			}

			g.bindings[toolboxMcpURLBindingKey] = attachment.McpURL
			injectMcpTool(g.managed, ref.Name, attachment.McpURL, attachment.ConnectionName)
			return nil
		},
	}
}

// foundryToolboxBuilder is the live toolboxBuilder backed by the Foundry
// toolbox data-plane endpoints. It holds no skills client: skills are published
// and pinned on the harness, never registered into a toolbox.
type foundryToolboxBuilder struct {
}

// resolvedSkill is a skill bundle matched to the version that its sibling
// `host: azure.ai.skill` service published.
type resolvedSkill struct {
	Name    string
	Version string
}

// resolveSkillMarkers maps each skills/<dir>/ bundle to the version its sibling
// azure.ai.skill service created, read from the deployment markers that service
// writes into the azd environment (SKILL_<NAME>_VERSION).
//
// azd does not upload skill bundles itself. Creating and versioning a Foundry
// skill belongs to the azure.ai.skills extension, which owns the
// `host: azure.ai.skill` service target; this extension only attaches the
// resulting versioned reference to the agent. A bundle with no marker means its
// service is missing from azure.yaml or has not been deployed yet, both of which
// the author has to fix.
func resolveSkillMarkers(
	skills []skillBundle,
	projectEndpoint string,
	env map[string]string,
) ([]resolvedSkill, error) {
	resolved := make([]resolvedSkill, 0, len(skills))
	for _, s := range skills {
		name := strings.TrimSpace(s.Meta.Name)
		if name == "" {
			name = s.Dir
		}
		versionKey := envkey.SkillVersion(name)
		version := strings.TrimSpace(env[versionKey])
		if version == "" {
			return nil, exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("skill %q has not been published (%s is not set)", name, versionKey),
				fmt.Sprintf(
					"add a service to azure.yaml with host: %s named %q, pointing archive: at the "+
						"%s/%s folder, and list %q in the agent service's uses:, then run "+
						"'azd deploy --all'. Re-running 'azd ai agent init' writes those entries for you",
					foundrySkillHost, name, promptSkillsDirName, s.Dir, name,
				),
			)
		}
		// The marker is scoped to the project it was created in. Reusing a
		// version id from a different Foundry project would pin the agent to a
		// skill that does not exist here, which the service reports as a
		// generic failure at run time rather than at deploy.
		projectKey := envkey.SkillProjectEndpoint(name)
		if declared := strings.TrimSpace(env[projectKey]); declared != "" &&
			!sameProjectEndpoint(declared, projectEndpoint) {
			return nil, exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("skill %q was published to a different Foundry project (%s)", name, projectKey),
				"run 'azd deploy --all' so the skill is republished to the project this agent targets",
			)
		}
		resolved = append(resolved, resolvedSkill{Name: name, Version: version})
	}
	return resolved, nil
}

// siblingToolboxEndpoint returns the MCP url that the toolbox's sibling
// `host: azure.ai.toolbox` service published into the azd environment, or an
// empty string when the toolbox has no sibling service.
//
// It also guards against a stale marker: the toolboxes extension records the
// project it deployed into alongside the endpoint, and an endpoint belonging to
// a different project would silently point the agent at a toolbox it cannot
// reach.
func siblingToolboxEndpoint(name, projectEndpoint string, env map[string]string) (string, error) {
	endpoint := strings.TrimSpace(env[envkey.ToolboxMCPEndpoint(name)])
	if endpoint == "" {
		return "", nil
	}
	projectKey := envkey.ToolboxProjectEndpoint(name)
	if declared := strings.TrimSpace(env[projectKey]); declared != "" &&
		!sameProjectEndpoint(declared, projectEndpoint) {
		return "", exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("toolbox %q was deployed to a different Foundry project (%s)", name, projectKey),
			"run 'azd deploy --all' so the toolbox is redeployed to the project this agent targets",
		)
	}
	return endpoint, nil
}

// ResolveToolbox consumes endpoint and connection outputs from sibling services.
func (b *foundryToolboxBuilder) ResolveToolbox(_ context.Context, ref toolboxRef) (toolboxAttachment, error) {
	if ref.MCPEndpoint == "" {
		return toolboxAttachment{}, exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("toolbox %q has not been deployed by an azure.ai.toolbox service", ref.Name),
			fmt.Sprintf("add %q to the agent service's uses list and run 'azd deploy --all'", ref.Name),
		)
	}
	return toolboxAttachment{McpURL: ref.MCPEndpoint, ConnectionName: ref.Connection}, nil
}

// newFoundryToolboxBuilder constructs the live builder from prompt settings.
func newFoundryToolboxBuilder(
	settings *PromptAgentSettings,
) (toolboxBuilder, error) {
	if settings == nil || strings.TrimSpace(settings.ProjectEndpoint) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required to resolve a toolbox",
			"run `azd up` to provision a Foundry project, or remove the 'toolbox:' block from agent.yaml",
		)
	}
	return &foundryToolboxBuilder{}, nil
}
