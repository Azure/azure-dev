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
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/servicekey"

	"github.com/braydonk/yaml"
)

const promptSkillsDirName = "skills"
const skillFileName = "SKILL.md"

type skillMeta struct {
	Name         string
	Description  string
	Version      string
	Instructions string
}

type skillBundle struct {
	Dir  string
	Path string
	Meta skillMeta
}

// SkillBundleRef describes a local skill bundle for init to declare as a sibling service.
type SkillBundleRef struct {
	Name        string
	Description string
	RelPath     string
}

// ScanSkillBundles returns bundles under agentDir/skills, sorted by folder name.
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
			Name: name, Description: b.Meta.Description, RelPath: promptSkillsDirName + "/" + b.Dir,
		})
	}
	return refs, nil
}

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
		// Reject symlinks so packaging a cloned project cannot upload unrelated local files.
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
	slices.SortFunc(bundles, func(a, b skillBundle) int { return strings.Compare(a.Dir, b.Dir) })
	return bundles, nil
}

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
	meta := skillMeta{Name: fm.Name, Description: fm.Description, Version: fm.Metadata.Version, Instructions: front.body}
	if strings.TrimSpace(meta.Description) == "" {
		return skillMeta{}, exterrors.Validation(
			exterrors.CodeInvalidAgentManifest,
			fmt.Sprintf("%s at %q is missing 'description'", skillFileName, path),
			"add a description to the SKILL.md frontmatter",
		)
	}
	return meta, nil
}

type frontmatterResult struct{ frontmatter, body string }

func extractFrontmatter(content string) (frontmatterResult, error) {
	trimmed := strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return frontmatterResult{}, fmt.Errorf("missing frontmatter delimiter")
	}
	rest := strings.TrimLeft(trimmed[len("---"):], "\r\n")
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return frontmatterResult{}, fmt.Errorf("unterminated frontmatter block")
	}
	// Consume only the closing fence line, not a leading bullet or break in the body.
	if nl := strings.IndexByte(after, '\n'); nl >= 0 {
		after = after[nl+1:]
	} else {
		after = ""
	}
	return frontmatterResult{frontmatter: before, body: after}, nil
}

func skillsShellNode(g *promptGraph, skills []skillBundle) *promptNode {
	if len(skills) == 0 {
		return nil
	}
	return &promptNode{
		Kind: nodeSkill, ID: promptSkillsDirName,
		Validate: func() error { return validateSkillBundleInstructions(skills) },
		Resolve:  func(ctx context.Context) error { return resolvePromptSkills(ctx, g, skills) },
	}
}

func skillsHarnessNode(g *promptGraph, skills []skillBundle) *promptNode {
	if len(skills) == 0 {
		return nil
	}
	return &promptNode{
		Kind: nodeSkill, ID: promptSkillsDirName,
		Validate: func() error { return validateSkillBundleInstructions(skills) },
		Resolve:  func(ctx context.Context) error { return resolvePromptSkills(ctx, g, skills) },
	}
}

func resolvePromptSkills(ctx context.Context, g *promptGraph, skills []skillBundle) error {
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

type resolvedSkill struct{ Name, Version string }

func resolveSkillMarkers(skills []skillBundle, projectEndpoint string, env map[string]string) ([]resolvedSkill, error) {
	resolved := make([]resolvedSkill, 0, len(skills))
	for _, s := range skills {
		name := promptSkillServiceName(s)
		versionKey := envkey.SkillVersion(name)
		version := strings.TrimSpace(env[versionKey])
		if version == "" {
			return nil, exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("skill %q has not been published (%s is not set)", name, versionKey),
				fmt.Sprintf("add a service to azure.yaml with host: %s named %q, pointing archive: at the "+
					"%s/%s folder, and list %q in the agent service's uses:, then run "+
					"'azd deploy --all'. Re-running 'azd ai agent init' writes those entries for you",
					foundrySkillHost, name, promptSkillsDirName, s.Dir, name),
			)
		}
		projectKey := envkey.SkillProjectEndpoint(name)
		if declared := strings.TrimSpace(env[projectKey]); declared != "" && !sameProjectEndpoint(declared, projectEndpoint) {
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
