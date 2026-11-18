// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/servicekey"
)

const promptSkillsDirName = "skills"

func skillNameMatches(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if strings.EqualFold(a, b) {
		return true
	}
	sa := servicekey.SanitizeServiceName(a)
	sb := servicekey.SanitizeServiceName(b)
	return sa != "" && strings.EqualFold(sa, sb)
}

func declaredVersionlessPromptSkills(managed *agent_yaml.PromptAgent) []string {
	if managed == nil || len(managed.Skills) == 0 {
		return nil
	}
	var declared []string
	for _, ref := range agent_yaml.PromptAgentSkillReferences(agent_yaml.PromptAgent{Skills: managed.Skills}) {
		if strings.TrimSpace(ref.Version) == "" {
			declared = append(declared, strings.TrimSpace(ref.Name))
		}
	}
	return declared
}

func skillsShellNode(g *promptGraph, skills []string) *promptNode {
	if len(skills) == 0 {
		return nil
	}
	return &promptNode{
		Kind:    nodeSkill,
		ID:      promptSkillsDirName,
		Resolve: func(ctx context.Context) error { return resolvePromptSkills(ctx, g, skills) },
	}
}

func skillsHarnessNode(g *promptGraph, skills []string) *promptNode {
	if len(skills) == 0 {
		return nil
	}
	return &promptNode{
		Kind:    nodeSkill,
		ID:      promptSkillsDirName,
		Resolve: func(ctx context.Context) error { return resolvePromptSkills(ctx, g, skills) },
	}
}

func resolvePromptSkills(ctx context.Context, g *promptGraph, skills []string) error {
	if err := validateSkillDependencies(ctx, g, skills); err != nil {
		return err
	}
	resolved, err := resolveSkillMarkers(skills, g.projectEndpoint(), g.env)
	if err != nil {
		return err
	}
	for _, s := range resolved {
		syncDeclaredSkillName(g.managed, s.AuthoredName, s.Name)
		addResolvedPromptSkill(g.managed, s.Name, s.Version)
	}
	return nil
}

func syncDeclaredSkillName(agent *agent_yaml.PromptAgent, authoredName, resolvedName string) {
	if agent == nil {
		return
	}
	for i, skill := range agent.Skills {
		if strings.TrimSpace(skill.Version) != "" {
			continue
		}
		if skillNameMatches(skill.Name, authoredName) || skillNameMatches(skill.Name, resolvedName) {
			agent.Skills[i].Name = resolvedName
		}
	}
}

func validateSkillDependencies(ctx context.Context, g *promptGraph, skills []string) error {
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

func promptSkillServiceName(skill string) string {
	return servicekey.SanitizeServiceName(skill)
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

type resolvedSkill struct{ AuthoredName, Name, Version string }

func resolveSkillMarkers(skills []string, projectEndpoint string, env map[string]string) ([]resolvedSkill, error) {
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
					"add an %s service named %q with 'azd ai skill add %s ...' and list %q in the "+
						"agent service's uses:, then run 'azd deploy --all'",
					foundrySkillHost, name, name, name,
				),
			)
		}
		projectKey := envkey.SkillProjectEndpoint(name)
		if declared := strings.TrimSpace(env[projectKey]); declared != "" &&
			!sameProjectEndpoint(declared, projectEndpoint) {
			return nil, exterrors.Dependency(
				exterrors.CodeFoundryDependencyNotReady,
				fmt.Sprintf("skill %q was published to a different Foundry project (%s)", name, projectKey),
				"run 'azd deploy --all' so the skill is republished to the project this agent targets",
			)
		}
		resolved = append(resolved, resolvedSkill{AuthoredName: s, Name: name, Version: version})
	}
	return resolved, nil
}
