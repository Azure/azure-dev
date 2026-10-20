// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/servicekey"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
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
	Name                string
	Version             string
	ProjectConnectionID string
	// MCPEndpoint is the toolbox's MCP url as published by its sibling
	// `host: azure.ai.toolbox` service, when that service deployed in this
	// environment. It is authoritative: the toolboxes extension owns the
	// toolbox's lifecycle and knows the endpoint it actually created, whereas
	// azd can only guess one from the name and version. Empty for a toolbox
	// that has no sibling service, e.g. one created outside of azure.yaml.
	MCPEndpoint string
}

// toolboxAttachment is the result of registering or resolving a toolbox: the
// MCP URL the agent connects to, the resolved toolbox resource name used as the
// MCP server label, and the project connection name or ID used to authenticate
// to that endpoint.
type toolboxAttachment struct {
	ServerLabel         string
	McpURL              string
	ProjectConnectionID string
}

// toolboxBuilder resolves an existing toolbox named by an explicit `toolbox:`
// reference, returning the toolbox MCP URL and its optional project connection
// reference. The seam keeps the graph node unit-testable without a live endpoint.
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

	f, err := os.Open(dir) //nolint:gosec // agentDir derives from the resolved service definition
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

// injectMcpTool ensures the agent's tools include an MCP tool for the given
// toolbox label and MCP URL. An existing MCP tool with the same server_url or
// server_label is updated in place (preserving any user-authored approval or
// option fields) rather than duplicated. When projectConnectionID is non-empty
// it is set as the tool's project_connection_id. The managed definition is
// mutated in place.
func injectMcpTool(managed *agent_yaml.PromptAgent, serverLabel, mcpURL, projectConnectionID string) {
	_ = reconcileMcpTool(managed, serverLabel, mcpURL, projectConnectionID)
}

func reconcileMcpTool(managed *agent_yaml.PromptAgent, serverLabel, mcpURL, projectConnectionID string) error {
	return reconcileMcpToolWithClaimed(managed, serverLabel, "", mcpURL, projectConnectionID, nil)
}

func reconcileMcpToolWithClaimed(
	managed *agent_yaml.PromptAgent,
	serverLabel, serviceName, mcpURL, projectConnectionID string,
	claimed map[int]struct{},
) error {
	if managed == nil || strings.TrimSpace(mcpURL) == "" {
		return nil
	}
	serverLabel = strings.TrimSpace(serverLabel)
	serviceName = strings.TrimSpace(serviceName)
	mcpURL = strings.TrimSpace(mcpURL)
	projectConnectionID = strings.TrimSpace(projectConnectionID)

	for i, raw := range managed.Tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", tool["type"]) != "mcp" {
			continue
		}
		existingURL, _ := tool["server_url"].(string)
		existingLabel, _ := tool["server_label"].(string)
		existingURL = strings.TrimSpace(existingURL)
		existingLabel = strings.TrimSpace(existingLabel)

		sameURL := existingURL == mcpURL
		if !sameURL && claimed != nil {
			if _, alreadyClaimed := claimed[i]; alreadyClaimed {
				continue
			}
		}

		sameLabel := existingLabel != "" &&
			(existingLabel == serverLabel || (serviceName != "" && existingLabel == serviceName))
		if !sameURL && !sameLabel {
			continue
		}

		if sameLabel && existingURL != "" && !sameURL && !isSameToolboxMCPEndpoint(existingURL, mcpURL) {
			return exterrors.Validation(
				exterrors.CodeInvalidAgentManifest,
				fmt.Sprintf(
					"MCP tool %q declares server_url %q, which conflicts with toolbox %q endpoint %q",
					existingLabel, existingURL, serverLabel, mcpURL,
				),
				fmt.Sprintf(
					"remove server_url from the %q MCP tool entry to use the deployed toolbox endpoint, "+
						"or give the custom MCP tool a distinct server_label",
					existingLabel,
				),
			)
		}

		urlChanged := existingURL != "" && existingURL != mcpURL
		if existingLabel == "" && serverLabel != "" {
			tool["server_label"] = serverLabel
		}
		tool["server_url"] = mcpURL
		if _, hasApproval := tool["require_approval"]; !hasApproval {
			tool["require_approval"] = "always"
		}
		if projectConnectionID != "" {
			existingConn, has := tool["project_connection_id"]
			if !has || strings.TrimSpace(fmt.Sprintf("%v", existingConn)) == "" || urlChanged {
				tool["project_connection_id"] = projectConnectionID
			}
		}
		if claimed != nil {
			claimed[i] = struct{}{}
		}
		return nil
	}
	mcpTool := map[string]any{
		"type":             "mcp",
		"server_label":     serverLabel,
		"server_url":       mcpURL,
		"require_approval": "always",
	}
	if projectConnectionID != "" {
		mcpTool["project_connection_id"] = projectConnectionID
	}
	managed.Tools = append(managed.Tools, mcpTool)
	if claimed != nil {
		claimed[len(managed.Tools)-1] = struct{}{}
	}
	return nil
}

func isSameToolboxMCPEndpoint(existingURL, newURL string) bool {
	existingName, _, err1 := parseToolboxMCPEndpoint(existingURL)
	newName, _, err2 := parseToolboxMCPEndpoint(newURL)
	if err1 != nil || err2 != nil || !strings.EqualFold(existingName, newName) {
		return false
	}
	existingHost, existingProject := foundryProjectIdentity(existingURL)
	newHost, newProject := foundryProjectIdentity(newURL)
	return existingHost != "" && existingProject != "" &&
		strings.EqualFold(existingHost, newHost) &&
		strings.EqualFold(existingProject, newProject)
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
	toolboxes []agent_yaml.ToolboxReference,
) *promptNode {
	if len(skills) == 0 && len(toolboxes) == 0 {
		return nil
	}
	return &promptNode{
		Kind: nodeSkill,
		ID:   promptSkillsDirName,
		Validate: func() error {
			// A toolbox is provisioned and reached through the harness sandbox.
			// Without a harness there is nothing to reach it from, so accepting
			// the reference would deploy an agent whose skills never run.
			if len(toolboxes) > 0 {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"toolboxes: is only available to an agent that names a harness",
					"add a 'harness:' block with type "+agent_api.ManagedAgentHarnessMicrosoftCopilot+
						" to the agent definition, or remove 'toolboxes:' and put the "+
						"skills in the agent project's skills/ folder",
				)
			}
			return validateSkillBundleInstructions(skills)
		},
		Resolve: func(ctx context.Context) error {
			if err := validateSkillDependencies(ctx, g, skills); err != nil {
				return err
			}
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
		Resolve: func(ctx context.Context) error {
			if err := validateSkillDependencies(ctx, g, skills); err != nil {
				return err
			}
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

func validateSkillDependencies(ctx context.Context, g *promptGraph, skills []skillBundle) error {
	if g == nil || g.agentService == nil || g.projectServices == nil {
		return nil
	}
	for _, skill := range skills {
		name := promptSkillServiceName(skill)
		service, exists := g.projectServices[name]
		if !exists || service.GetHost() != foundrySkillHost {
			return exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("skill %q has no enabled %s sibling service", name, foundrySkillHost),
				fmt.Sprintf("add an %s service named %q and run 'azd deploy --all'", foundrySkillHost, name),
			)
		}
		if !slices.Contains(g.agentService.GetUses(), name) {
			return exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("skill service %q is not declared in agent service %q uses", name, g.agentService.GetName()),
				fmt.Sprintf("add %q to the agent service's uses list and run 'azd deploy --all'", name),
			)
		}
		if g.dependencyEnabled != nil {
			enabled, err := g.dependencyEnabled(ctx, name)
			if err != nil {
				return err
			}
			if !enabled {
				return exterrors.Dependency(
					exterrors.CodeFoundryDependencyNotReady,
					fmt.Sprintf("skill service %q is disabled by its deployment condition", name),
					"enable the skill dependency and run 'azd deploy --all'",
				)
			}
		}
	}
	return nil
}

func promptSkillServiceName(skill skillBundle) string {
	name := strings.TrimSpace(skill.Meta.Name)
	if name == "" {
		name = skill.Dir
	}
	return servicekey.SanitizeServiceName(name)
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

// toolboxNode attaches existing shared toolboxes by reference, as mcp tools.
//
// It is reachable only from an explicit `toolboxes:` block in the agent definition. Skills
// no longer travel this path: every harnessed agent already gets a system
// toolbox whose name, version, endpoint and lifecycle the service owns, so azd
// creating a second toolbox of its own to carry skills both duplicated that and
// left the skills invisible as skills.
func toolboxNode(
	g *promptGraph,
	refs []agent_yaml.ToolboxReference,
	newBuilder func() (toolboxBuilder, error),
) *promptNode {
	if len(refs) == 0 {
		return nil
	}
	return &promptNode{
		Kind: nodeToolbox,
		ID:   strings.TrimSpace(refs[0].Name),
		Validate: func() error {
			for _, ref := range refs {
				if strings.TrimSpace(ref.Name) == "" {
					return exterrors.Validation(
						exterrors.CodeInvalidAgentManifest,
						"toolbox reference is missing a name",
						"set each entry under 'toolboxes:' to a non-empty azure.ai.toolbox service name",
					)
				}
			}
			return nil
		},
		Resolve: func(ctx context.Context) error {
			if err := validateToolboxDependencies(ctx, g, refs); err != nil {
				return err
			}
			builder, err := newBuilder()
			if err != nil {
				return err
			}
			type resolvedToolboxItem struct {
				serviceName string
				attachment  toolboxAttachment
			}
			var items []resolvedToolboxItem
			urlsByLabel := map[string]map[string]struct{}{}
			seen := make(map[string]struct{}, len(refs))
			for _, ref := range refs {
				serviceName := strings.TrimSpace(ref.Name)
				if _, ok := seen[serviceName]; ok {
					continue
				}
				seen[serviceName] = struct{}{}
				mcpEndpoint, err := siblingToolboxEndpoint(serviceName, g.projectEndpoint(), g.env)
				if err != nil {
					return err
				}
				attachment, err := builder.ResolveToolbox(ctx, toolboxRef{
					Name:        serviceName,
					MCPEndpoint: mcpEndpoint,
				})
				if err != nil {
					return err
				}
				items = append(items, resolvedToolboxItem{
					serviceName: serviceName,
					attachment:  attachment,
				})
				baseLabel := strings.TrimSpace(attachment.ServerLabel)
				if baseLabel == "" {
					baseLabel = serviceName
				}
				labelKey := strings.ToLower(baseLabel)
				if urlsByLabel[labelKey] == nil {
					urlsByLabel[labelKey] = map[string]struct{}{}
				}
				urlsByLabel[labelKey][strings.TrimSpace(attachment.McpURL)] = struct{}{}
			}

			claimed := make(map[int]struct{}, len(items))
			for _, item := range items {
				g.bindings[toolboxMcpURLBindingKey] = item.attachment.McpURL
				label := strings.TrimSpace(item.attachment.ServerLabel)
				if label == "" || len(urlsByLabel[strings.ToLower(label)]) > 1 {
					label = item.serviceName
				}
				if err := reconcileMcpToolWithClaimed(
					g.managed,
					label,
					item.serviceName,
					item.attachment.McpURL,
					item.attachment.ProjectConnectionID,
					claimed,
				); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func validateToolboxDependencies(
	ctx context.Context,
	g *promptGraph,
	refs []agent_yaml.ToolboxReference,
) error {
	if g == nil || g.agentService == nil || g.projectServices == nil {
		return nil
	}
	for _, ref := range refs {
		name := strings.TrimSpace(ref.Name)
		service, exists := g.projectServices[name]
		if !exists {
			return exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("toolbox %q has no %s sibling service in azure.yaml", name, foundryToolboxHost),
				fmt.Sprintf(
					"add an %s service named %q, add it to the agent service's uses list, and run 'azd deploy --all'",
					foundryToolboxHost, name,
				),
			)
		}
		if service.GetHost() != foundryToolboxHost {
			return exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf(
					"toolbox reference %q resolves to service host %q instead of %q",
					name, service.GetHost(), foundryToolboxHost,
				),
				fmt.Sprintf(
					"change the %q service host to %q or remove it from the agent toolboxes",
					name, foundryToolboxHost,
				),
			)
		}
		if !slices.Contains(g.agentService.GetUses(), name) {
			return exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("toolbox service %q is not declared in agent service %q uses", name, g.agentService.GetName()),
				fmt.Sprintf("add %q to the agent service's uses list and run 'azd deploy --all'", name),
			)
		}
		if g.dependencyEnabled != nil {
			enabled, err := g.dependencyEnabled(ctx, name)
			if err != nil {
				return err
			}
			if !enabled {
				return exterrors.Dependency(
					exterrors.CodeFoundryDependencyNotReady,
					fmt.Sprintf("toolbox service %q is disabled by its deployment condition", name),
					"enable the toolbox dependency and run 'azd deploy --all'",
				)
			}
		}
	}
	return nil
}

const (
	toolboxMCPConnectionCategory = "RemoteTool"
	toolboxMCPConnectionAuthType = "UserEntraToken"
	toolboxMCPConnectionAudience = "https://ai.azure.com"
	toolboxMCPMetadataType       = "toolbox_MCP"
	armConnectionsAPIVersion     = "2025-04-01-preview"
)

type toolboxConnectionProperties struct {
	Category string            `json:"category"`
	AuthType string            `json:"authType"`
	Audience string            `json:"audience,omitempty"`
	Target   string            `json:"target"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type toolboxConnectionBody struct {
	Name       string                      `json:"name,omitempty"`
	Properties toolboxConnectionProperties `json:"properties"`
}

// foundryToolboxBuilder is the live toolboxBuilder that resolves each toolbox's
// deployed MCP endpoint and ensures a compatible UserEntraToken project
// connection exists on the Foundry project.
type foundryToolboxBuilder struct {
	subscriptionID string
	resourceGroup  string
	accountName    string
	projectName    string
	pipeline       runtime.Pipeline
	explicitConns  map[string]struct{}
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
		name := promptSkillServiceName(s)
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

// parseToolboxMCPEndpoint extracts the deployed toolbox resource name and version
// from a Foundry toolbox MCP endpoint URL of the form:
//
//	https://<account>.services.ai.azure.com/api/projects/<project>/toolboxes/<name>/versions/<version>/mcp?api-version=v1
func parseToolboxMCPEndpoint(mcpURL string) (string, string, error) {
	if err := validateAuthenticatedPromptEndpoint(mcpURL); err != nil {
		return "", "", fmt.Errorf("endpoint must be an HTTPS Foundry project URL (*.services.ai.azure.com)")
	}
	if _, project := foundryProjectIdentity(mcpURL); strings.TrimSpace(project) == "" {
		return "", "", fmt.Errorf("endpoint path must include /api/projects/<project>")
	}
	u, err := url.Parse(strings.TrimSpace(mcpURL))
	if err != nil {
		return "", "", fmt.Errorf("endpoint must be a valid HTTPS URL")
	}
	escapedPath := u.EscapedPath()
	if escapedPath == "" {
		escapedPath = u.Path
	}
	const segment = "/toolboxes/"
	_, after, ok := strings.Cut(escapedPath, segment)
	if !ok {
		return "", "", fmt.Errorf("endpoint path must contain /toolboxes/<name>/versions/<version>/mcp")
	}
	rest := strings.Trim(after, "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || parts[1] != "versions" || parts[3] != "mcp" {
		return "", "", fmt.Errorf("endpoint path must match /toolboxes/<name>/versions/<version>/mcp")
	}
	toolboxName, err := url.PathUnescape(parts[0])
	if err != nil {
		return "", "", fmt.Errorf("invalid toolbox name segment: %w", err)
	}
	toolboxVersion, err := url.PathUnescape(parts[2])
	if err != nil {
		return "", "", fmt.Errorf("invalid toolbox version segment: %w", err)
	}
	toolboxName = strings.TrimSpace(toolboxName)
	toolboxVersion = strings.TrimSpace(toolboxVersion)
	if toolboxName == "" || toolboxVersion == "" {
		return "", "", fmt.Errorf("endpoint must include a non-empty toolbox name and version")
	}
	return toolboxName, toolboxVersion, nil
}

// ResolveToolbox consumes the endpoint published by the sibling toolbox service,
// creates or reuses a UserEntraToken project connection for that toolbox MCP URL,
// and returns the attachment to wire onto the agent's MCP tool.
func (b *foundryToolboxBuilder) ResolveToolbox(ctx context.Context, ref toolboxRef) (toolboxAttachment, error) {
	if strings.TrimSpace(ref.MCPEndpoint) == "" {
		return toolboxAttachment{}, exterrors.Dependency(
			exterrors.CodeFoundryDependencyNotReady,
			fmt.Sprintf("toolbox %q has not been deployed by an azure.ai.toolbox service", ref.Name),
			fmt.Sprintf("add %q to the agent service's uses list and run 'azd deploy --all'", ref.Name),
		)
	}
	toolboxName, toolboxVersion, err := parseToolboxMCPEndpoint(ref.MCPEndpoint)
	if err != nil {
		return toolboxAttachment{}, exterrors.Validation(
			exterrors.CodeInvalidToolbox,
			fmt.Sprintf("toolbox %q published an invalid MCP endpoint %q: %s", ref.Name, ref.MCPEndpoint, err),
			"redeploy the toolbox service with 'azd deploy --all' or check its configured endpoint",
		)
	}

	connectionName, err := b.ensureToolboxConnection(ctx, toolboxName, toolboxVersion, ref.MCPEndpoint)
	if err != nil {
		return toolboxAttachment{}, err
	}
	return toolboxAttachment{
		ServerLabel:         toolboxName,
		McpURL:              ref.MCPEndpoint,
		ProjectConnectionID: connectionName,
	}, nil
}

func (b *foundryToolboxBuilder) connectionURL(connectionName string) string {
	return fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/"+
			"providers/Microsoft.CognitiveServices/accounts/%s/projects/%s/"+
			"connections/%s?api-version=%s",
		url.PathEscape(b.subscriptionID),
		url.PathEscape(b.resourceGroup),
		url.PathEscape(b.accountName),
		url.PathEscape(b.projectName),
		url.PathEscape(connectionName),
		armConnectionsAPIVersion,
	)
}

func (b *foundryToolboxBuilder) ensureToolboxConnection(
	ctx context.Context,
	toolboxName, toolboxVersion, mcpURL string,
) (string, error) {
	desired := toolboxConnectionProperties{
		Category: toolboxMCPConnectionCategory,
		AuthType: toolboxMCPConnectionAuthType,
		Audience: toolboxMCPConnectionAudience,
		Target:   mcpURL,
		Metadata: map[string]string{
			"type":           toolboxMCPMetadataType,
			"toolboxName":    toolboxName,
			"toolboxVersion": toolboxVersion,
		},
	}

	candidates := []string{
		toolboxName + "-mcp",
		fmt.Sprintf("%s-v%s-mcp", toolboxName, toolboxVersion),
	}
	for i, connectionName := range candidates {
		_, isExplicit := b.explicitConns[strings.ToLower(connectionName)]
		existing, found, err := b.getConnection(ctx, connectionName)
		if err != nil {
			return "", err
		}
		if found {
			if isMatchingToolboxConnection(existing, mcpURL, toolboxName, toolboxVersion) {
				return connectionName, nil
			}
			// If the base <toolbox>-mcp connection is bound to another version of
			// this same toolbox, preserve it for existing agents/versions and fall
			// back to the version-scoped connection name.
			if i == 0 && !isExplicit && isOtherVersionToolboxConnection(existing, mcpURL, toolboxName) {
				continue
			}
			return "", exterrors.Validation(
				exterrors.CodeInvalidConnection,
				fmt.Sprintf(
					"project connection %q already exists with incompatible target or authentication settings for toolbox %q",
					connectionName, toolboxName,
				),
				fmt.Sprintf(
					"remove or rename the conflicting connection %q in the Foundry project (or azure.yaml), then retry 'azd deploy'",
					connectionName,
				),
			)
		}
		if isExplicit {
			return "", exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf(
					"connection %q is explicitly declared in azure.yaml and cannot be overwritten for toolbox %q",
					connectionName, toolboxName,
				),
				fmt.Sprintf(
					"rename the azure.ai.connection service %q or deploy a compatible UserEntraToken connection for %q first",
					connectionName, toolboxName,
				),
			)
		}

		if err := b.putConnection(ctx, connectionName, desired); err != nil {
			return "", err
		}
		return connectionName, nil
	}

	return "", exterrors.Internal(
		exterrors.CodeConnectionCreationFail,
		fmt.Sprintf("failed to resolve a connection name for toolbox %q", toolboxName),
	)
}

func isMatchingToolboxConnection(
	existing *toolboxConnectionProperties,
	mcpURL, toolboxName, toolboxVersion string,
) bool {
	if !hasToolboxMCPConnectionAuth(existing) {
		return false
	}
	if strings.TrimSpace(existing.Target) != strings.TrimSpace(mcpURL) {
		return false
	}
	metaType := strings.TrimSpace(existing.Metadata["type"])
	metaName := strings.TrimSpace(existing.Metadata["toolboxName"])
	metaVersion := strings.TrimSpace(existing.Metadata["toolboxVersion"])
	if (metaType != "" && metaType != toolboxMCPMetadataType) ||
		(metaName != "" && metaName != toolboxName) ||
		(metaVersion != "" && metaVersion != toolboxVersion) {
		return false
	}
	return true
}

func isOtherVersionToolboxConnection(
	existing *toolboxConnectionProperties,
	mcpURL, toolboxName string,
) bool {
	if !hasToolboxMCPConnectionAuth(existing) {
		return false
	}
	if strings.TrimSpace(existing.Target) == strings.TrimSpace(mcpURL) {
		return false
	}
	metaType := strings.TrimSpace(existing.Metadata["type"])
	metaName := strings.TrimSpace(existing.Metadata["toolboxName"])
	if metaType == toolboxMCPMetadataType && metaName == toolboxName {
		return true
	}
	if metaType == "" && metaName == "" {
		parsedName, _, err := parseToolboxMCPEndpoint(existing.Target)
		return err == nil && parsedName == toolboxName
	}
	return false
}

func hasToolboxMCPConnectionAuth(existing *toolboxConnectionProperties) bool {
	if existing == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(existing.Category), toolboxMCPConnectionCategory) ||
		!strings.EqualFold(strings.TrimSpace(existing.AuthType), toolboxMCPConnectionAuthType) {
		return false
	}
	aud := strings.TrimRight(strings.TrimSpace(existing.Audience), "/")
	return aud == "" || strings.EqualFold(aud, toolboxMCPConnectionAudience)
}

func (b *foundryToolboxBuilder) getConnection(
	ctx context.Context,
	connectionName string,
) (*toolboxConnectionProperties, bool, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, b.connectionURL(connectionName))
	if err != nil {
		return nil, false, fmt.Errorf("creating get-connection request for %q: %w", connectionName, err)
	}
	resp, err := b.pipeline.Do(req)
	if err != nil {
		return nil, false, exterrors.ServiceFromAzure(err, exterrors.OpEnsureToolboxConnection)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, exterrors.ServiceFromAzure(runtime.NewResponseError(resp), exterrors.OpEnsureToolboxConnection)
	}

	var body toolboxConnectionBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, false, fmt.Errorf("decoding connection %q response: %w", connectionName, err)
	}
	return &body.Properties, true, nil
}

func (b *foundryToolboxBuilder) putConnection(
	ctx context.Context,
	connectionName string,
	props toolboxConnectionProperties,
) error {
	body := toolboxConnectionBody{
		Name:       connectionName,
		Properties: props,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling connection %q body: %w", connectionName, err)
	}

	req, err := runtime.NewRequest(ctx, http.MethodPut, b.connectionURL(connectionName))
	if err != nil {
		return fmt.Errorf("creating put-connection request for %q: %w", connectionName, err)
	}
	if err := req.SetBody(
		streaming.NopCloser(bytes.NewReader(bodyBytes)),
		"application/json",
	); err != nil {
		return fmt.Errorf("setting put-connection request body for %q: %w", connectionName, err)
	}

	resp, err := b.pipeline.Do(req)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpEnsureToolboxConnection)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return exterrors.ServiceFromAzure(runtime.NewResponseError(resp), exterrors.OpEnsureToolboxConnection)
	}
	return nil
}

func foundryAccountAndProject(projectEndpoint string) (string, string) {
	host, project := foundryProjectIdentity(projectEndpoint)
	if host == "" || project == "" {
		return "", ""
	}
	const suffix = ".services.ai.azure.com"
	if !strings.HasSuffix(strings.ToLower(host), suffix) {
		return "", ""
	}
	account := strings.TrimSpace(host[:len(host)-len(suffix)])
	return account, strings.TrimSpace(project)
}

func resolveToolboxProjectARMTarget(
	g *promptGraph,
	settings *PromptAgentSettings,
) (subscriptionID, resourceGroup, accountName, projectName string, err error) {
	subscriptionID = strings.TrimSpace(settings.SubscriptionID)
	resourceGroup = strings.TrimSpace(settings.ResourceGroup)
	accountName, projectName = foundryAccountAndProject(settings.ProjectEndpoint)

	if g != nil && g.env != nil {
		if acct := strings.TrimSpace(g.env["AZURE_AI_ACCOUNT_NAME"]); acct != "" {
			accountName = acct
		}
		if proj := strings.TrimSpace(g.env["AZURE_AI_PROJECT_NAME"]); proj != "" {
			projectName = proj
		}
		if projectID := strings.TrimSpace(g.env["AZURE_AI_PROJECT_ID"]); projectID != "" {
			parsed, parseErr := arm.ParseResourceID(projectID)
			if parseErr != nil || parsed.Parent == nil ||
				!strings.EqualFold(parsed.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") {
				return "", "", "", "", exterrors.Validation(
					exterrors.CodeInvalidAiProjectId,
					fmt.Sprintf("AZURE_AI_PROJECT_ID is not a valid Foundry project resource ID: %q", projectID),
					"set AZURE_AI_PROJECT_ID to a Microsoft.CognitiveServices/accounts/projects resource ID",
				)
			}
			subscriptionID = strings.TrimSpace(parsed.SubscriptionID)
			resourceGroup = strings.TrimSpace(parsed.ResourceGroupName)
			accountName = strings.TrimSpace(parsed.Parent.Name)
			projectName = strings.TrimSpace(parsed.Name)
		}
	}

	if subscriptionID == "" || resourceGroup == "" || accountName == "" || projectName == "" {
		return "", "", "", "", exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"Foundry project subscription, resource group, account, and project are required to configure toolbox connections",
			"run `azd provision` to populate the Foundry project environment values",
		)
	}
	return subscriptionID, resourceGroup, accountName, projectName, nil
}

// newFoundryToolboxBuilder constructs the live builder from prompt settings.
func newFoundryToolboxBuilder(
	g *promptGraph,
	settings *PromptAgentSettings,
	credential azcore.TokenCredential,
) (toolboxBuilder, error) {
	if settings == nil || strings.TrimSpace(settings.ProjectEndpoint) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required to resolve a toolbox",
			"run `azd up` to provision a Foundry project, or remove the 'toolboxes:' block from the agent definition",
		)
	}
	subscriptionID, resourceGroup, accountName, projectName, err := resolveToolboxProjectARMTarget(g, settings)
	if err != nil {
		return nil, err
	}
	if credential == nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			"an Azure credential is required to configure toolbox connections",
			"run `azd auth login` to authenticate",
		)
	}

	explicitConns := map[string]struct{}{}
	if g != nil && g.projectServices != nil {
		identities, err := foundryConnectionServiceIdentities(g.projectServices, g.projectRoot)
		if err != nil {
			return nil, err
		}
		for _, id := range identities {
			if name := strings.ToLower(strings.TrimSpace(id.ResourceName)); name != "" {
				explicitConns[name] = struct{}{}
			}
		}
	}

	armOpts := azure.NewArmClientOptions()
	pipeline := runtime.NewPipeline(
		"azure-ai-agents",
		"v1.0.0",
		runtime.PipelineOptions{
			PerCall: []policy.Policy{
				runtime.NewBearerTokenPolicy(
					credential,
					[]string{"https://management.azure.com/.default"},
					nil,
				),
			},
		},
		&armOpts.ClientOptions,
	)

	return &foundryToolboxBuilder{
		subscriptionID: subscriptionID,
		resourceGroup:  resourceGroup,
		accountName:    accountName,
		projectName:    projectName,
		pipeline:       pipeline,
		explicitConns:  explicitConns,
	}, nil
}
