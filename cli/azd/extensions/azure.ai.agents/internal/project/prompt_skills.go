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
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/azure"

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
	Name    string
	Version string
}

// toolboxBuilder registers skills into a toolbox version (primary path) or
// resolves an existing toolbox (reference path), returning the toolbox MCP url.
// The seam keeps the graph node unit-testable without a live endpoint.
type toolboxBuilder interface {
	// EnsureToolbox registers the skills into a toolbox named toolboxName and
	// returns its MCP url.
	EnsureToolbox(ctx context.Context, toolboxName string, skills []skillBundle) (mcpURL string, err error)
	// ResolveToolbox returns the MCP url of an existing toolbox version.
	ResolveToolbox(ctx context.Context, ref toolboxRef) (mcpURL string, err error)
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
		info, statErr := os.Stat(bundleDir)
		if statErr != nil {
			return nil, fmt.Errorf("stat %q: %w", bundleDir, statErr)
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
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return frontmatterResult{}, fmt.Errorf("unterminated frontmatter block")
	}
	front := rest[:end]
	// The body starts after the closing `---` line.
	after := rest[end+len("\n---"):]
	after = strings.TrimPrefix(after, "-")   // tolerate longer --- fences
	after = strings.TrimLeft(after, "-\r\n") // consume the rest of the fence line
	return frontmatterResult{frontmatter: front, body: strings.TrimLeft(after, "\r\n")}, nil
}

// injectMcpTool ensures the agent's tools include an mcp tool for the given
// toolbox label and MCP url. An existing mcp tool with the same server_url is
// left in place (not duplicated). The managed definition is mutated in place.
func injectMcpTool(managed *agent_yaml.PromptAgent, serverLabel, mcpURL string) {
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
			return // already present
		}
	}
	managed.Tools = append(managed.Tools, map[string]any{
		"type":             "mcp",
		"server_label":     serverLabel,
		"server_url":       mcpURL,
		"require_approval": "always",
	})
}

// toolboxNode builds the skill/toolbox graph node. When ref is non-nil the
// existing toolbox is attached by reference; otherwise the skill bundles are
// registered into a new toolbox version. Returns nil when there is nothing to
// attach (no skills and no reference).
func toolboxNode(
	g *promptGraph,
	skills []skillBundle,
	ref *agent_yaml.ToolboxReference,
	newBuilder func() (toolboxBuilder, error),
) *promptNode {
	if len(skills) == 0 && ref == nil {
		return nil
	}
	return &promptNode{
		Kind: nodeToolbox,
		ID:   promptSkillsDirName,
		Validate: func() error {
			// SKILL.md parsing already validated name/description/body in
			// scanSkillsDir. Version is optional (service-assigned), so nothing
			// further to check per-skill here.
			for _, s := range skills {
				if strings.TrimSpace(s.Meta.Instructions) == "" {
					return exterrors.Validation(
						exterrors.CodeInvalidAgentManifest,
						fmt.Sprintf("skill %q has no instructions (empty SKILL.md body)", s.Dir),
						"add Markdown content below the frontmatter in the skill's SKILL.md",
					)
				}
			}
			if ref != nil && strings.TrimSpace(ref.Name) == "" {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					"toolbox reference is missing a name",
					"set toolbox.name in agent.yaml",
				)
			}
			return nil
		},
		Resolve: func(ctx context.Context) error {
			builder, err := newBuilder()
			if err != nil {
				return err
			}

			var (
				mcpURL string
				label  string
			)
			if ref != nil {
				label = ref.Name
				mcpURL, err = builder.ResolveToolbox(ctx, toolboxRef{Name: ref.Name, Version: ref.Version})
			} else {
				label = g.managed.Name
				mcpURL, err = builder.EnsureToolbox(ctx, g.managed.Name, skills)
			}
			if err != nil {
				return err
			}

			g.bindings[toolboxMcpURLBindingKey] = mcpURL
			injectMcpTool(g.managed, label, mcpURL)
			return nil
		},
	}
}

// foundryToolboxBuilder is the live toolboxBuilder backed by the Foundry skill
// and toolbox data-plane endpoints.
type foundryToolboxBuilder struct {
	skills          *azure.FoundrySkillsClient
	toolboxes       *azure.FoundryToolboxClient
	projectEndpoint string
}

// EnsureToolbox registers each skill bundle at its pinned version, creates a
// toolbox version referencing them, and returns the toolbox MCP url.
func (b *foundryToolboxBuilder) EnsureToolbox(
	ctx context.Context, toolboxName string, skills []skillBundle,
) (string, error) {
	// Skills are attached to a toolbox via a separate `skills` array of skill
	// references (distinct from `tools`), per the Foundry Skills API.
	skillRefs := make([]map[string]any, 0, len(skills))
	for _, s := range skills {
		// Upload every file in the bundle (SKILL.md plus any references/,
		// assets/, or other supporting files), not just SKILL.md. The service
		// parses SKILL.md itself from the uploaded bundle; using the JSON
		// inline_content path here would silently drop everything except
		// SKILL.md's body.
		files, err := readSkillBundleFiles(s.Path)
		if err != nil {
			return "", err
		}

		version, err := b.skills.CreateSkillVersionFromFiles(ctx, s.Meta.Name, files)
		if err != nil {
			return "", fmt.Errorf("registering skill %q: %w", s.Meta.Name, err)
		}

		// Creating a version does NOT make it the skill's default_version —
		// the Foundry API only auto-promotes the very first version. Without
		// this, redeploying with changed skill content registers a new
		// version that the Foundry portal's skill view (and any unversioned
		// reference) never surfaces, making the update look like it didn't
		// happen. Promote every newly created version to default so the
		// latest deploy is always what's active.
		if err := b.skills.PromoteSkillVersion(ctx, version.Name, version.Version); err != nil {
			return "", fmt.Errorf("promoting skill %q to version %s: %w", s.Meta.Name, version.Version, err)
		}

		ref := map[string]any{
			"type": "skill_reference",
			"name": version.Name,
		}
		// Pin the reference to the created version only when the author pinned a
		// version; otherwise follow the skill's default_version.
		if strings.TrimSpace(s.Meta.Version) != "" {
			ref["version"] = version.Version
		}
		skillRefs = append(skillRefs, ref)
	}

	created, err := b.toolboxes.CreateToolboxVersion(ctx, toolboxName, &azure.CreateToolboxVersionRequest{
		Tools:  []map[string]any{},
		Skills: skillRefs,
	})
	if err != nil {
		return "", fmt.Errorf("creating toolbox version: %w", err)
	}

	// Same reasoning as the skill promotion above: creating a toolbox version
	// doesn't promote it, so the toolbox consumer endpoint (and the portal)
	// would keep serving the previous version's tool/skill set otherwise.
	if err := b.toolboxes.PromoteToolboxVersion(ctx, toolboxName, created.Version); err != nil {
		return "", fmt.Errorf("promoting toolbox %q to version %s: %w", toolboxName, created.Version, err)
	}

	return b.mcpURL(created.Name, created.Version), nil
}

// ResolveToolbox confirms an existing toolbox and returns its MCP url. When the
// reference pins a version, the version-specific (developer) endpoint is used;
// otherwise the consumer endpoint that always serves the default_version.
func (b *foundryToolboxBuilder) ResolveToolbox(ctx context.Context, ref toolboxRef) (string, error) {
	if _, err := b.toolboxes.GetToolbox(ctx, ref.Name); err != nil {
		return "", fmt.Errorf("resolving toolbox %q: %w", ref.Name, err)
	}
	return b.mcpURL(ref.Name, ref.Version), nil
}

// mcpURL builds the toolbox MCP endpoint. With a version it returns the
// version-specific (developer) endpoint; without one it returns the consumer
// endpoint that always serves the toolbox's default_version. Both carry the
// required api-version query parameter.
func (b *foundryToolboxBuilder) mcpURL(name, version string) string {
	base := strings.TrimRight(b.projectEndpoint, "/")
	if strings.TrimSpace(version) == "" {
		return fmt.Sprintf("%s/toolboxes/%s/mcp?api-version=%s", base, name, toolboxMcpApiVersion)
	}
	return fmt.Sprintf(
		"%s/toolboxes/%s/versions/%s/mcp?api-version=%s",
		base, name, version, toolboxMcpApiVersion,
	)
}

// toolboxMcpApiVersion is the api-version query parameter required on toolbox
// MCP endpoint URLs.
const toolboxMcpApiVersion = "v1"

// readSkillBundleFiles reads every file under a skill bundle directory —
// SKILL.md plus any references/, assets/, or other supporting files, at any
// nesting depth — into a map of bundle-relative path (forward-slash
// separated) to raw content, so the entire bundle can be uploaded together
// via the multipart skill-version API. Without this, only SKILL.md would ever
// reach the service and any files it references (scripts, docs, assets)
// would be silently dropped.
func readSkillBundleFiles(bundleDir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(bundleDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, readErr := os.ReadFile(path) //nolint:gosec // path derived from the agent's skills/ folder
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(bundleDir, path)
		if relErr != nil {
			return relErr
		}
		files[filepath.ToSlash(rel)] = content
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading skill bundle %q: %w", bundleDir, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("skill bundle %q contains no files", bundleDir)
	}
	return files, nil
}

// newFoundryToolboxBuilder constructs the live builder from prompt settings.
func newFoundryToolboxBuilder(settings *PromptAgentSettings) (toolboxBuilder, error) {
	if settings == nil || strings.TrimSpace(settings.ProjectEndpoint) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"a Foundry project endpoint is required to register skills / resolve a toolbox",
			"run `azd up` to provision a Foundry project, or remove the skills/ folder",
		)
	}
	cred := promptCredential()
	return &foundryToolboxBuilder{
		skills:          azure.NewFoundrySkillsClient(settings.ProjectEndpoint, cred),
		toolboxes:       azure.NewFoundryToolboxClient(settings.ProjectEndpoint, cred),
		projectEndpoint: settings.ProjectEndpoint,
	}, nil
}
