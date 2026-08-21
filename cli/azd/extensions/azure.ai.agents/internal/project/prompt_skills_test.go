// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
)

// fakeToolboxBuilder records calls and returns a fixed MCP url.
type fakeToolboxBuilder struct {
	mcpURL       string
	connName     string
	resolveCalls int
	lastRef      toolboxRef
}

func (b *fakeToolboxBuilder) ResolveToolbox(_ context.Context, ref toolboxRef) (toolboxAttachment, error) {
	b.resolveCalls++
	b.lastRef = ref
	if b.mcpURL == "" {
		b.mcpURL = "https://proj/toolboxes/existing/versions/2/mcp"
	}
	return toolboxAttachment{McpURL: b.mcpURL, ConnectionName: b.connName}, nil
}

func writeSkillsDir(t *testing.T, skills map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if skills == nil {
		return dir
	}
	for name, skillMD := range skills {
		bundle := filepath.Join(dir, "skills", name)
		if err := os.MkdirAll(bundle, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(bundle, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
			t.Fatalf("write SKILL.md: %v", err)
		}
	}
	return dir
}

const validSkillMD = `---
name: agentdevcompute
description: Helps with dev compute tasks.
metadata:
  version: 1.2.0
---
# Body
Some skill instructions.
`

func TestParseSkillMD_Valid(t *testing.T) {
	dir := writeSkillsDir(t, map[string]string{"agentdevcompute": validSkillMD})
	meta, err := parseSkillMD(filepath.Join(dir, "skills", "agentdevcompute", "SKILL.md"))
	if err != nil {
		t.Fatalf("parseSkillMD: %v", err)
	}
	if meta.Name != "agentdevcompute" || meta.Description == "" || meta.Version != "1.2.0" {
		t.Errorf("meta: got %+v", meta)
	}
	if !strings.Contains(meta.Instructions, "Some skill instructions.") {
		t.Errorf("instructions body not captured: got %q", meta.Instructions)
	}
}

func TestParseSkillMD_VersionOptional(t *testing.T) {
	md := `---
name: s
description: has no version, which is allowed
---
body content
`
	dir := writeSkillsDir(t, map[string]string{"s": md})
	meta, err := parseSkillMD(filepath.Join(dir, "skills", "s", "SKILL.md"))
	if err != nil {
		t.Fatalf("version should be optional: %v", err)
	}
	if meta.Version != "" {
		t.Errorf("expected empty version, got %q", meta.Version)
	}
	if !strings.Contains(meta.Instructions, "body content") {
		t.Errorf("instructions: got %q", meta.Instructions)
	}
}

func TestParseSkillMD_MissingDescription(t *testing.T) {
	md := `---
name: s
metadata:
  version: 1.0.0
---
body
`
	dir := writeSkillsDir(t, map[string]string{"s": md})
	_, err := parseSkillMD(filepath.Join(dir, "skills", "s", "SKILL.md"))
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("expected description error, got %v", err)
	}
}

func TestParseSkillMD_NoFrontmatter(t *testing.T) {
	md := "# Just a heading\nno frontmatter\n"
	dir := writeSkillsDir(t, map[string]string{"s": md})
	_, err := parseSkillMD(filepath.Join(dir, "skills", "s", "SKILL.md"))
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestScanSkillsDir_MultipleBundlesSorted(t *testing.T) {
	skillB := strings.Replace(validSkillMD, "agentdevcompute", "bravo", 1)
	skillA := strings.Replace(validSkillMD, "agentdevcompute", "alpha", 1)
	dir := writeSkillsDir(t, map[string]string{"bravo": skillB, "alpha": skillA})

	bundles, err := scanSkillsDir(dir)
	if err != nil {
		t.Fatalf("scanSkillsDir: %v", err)
	}
	if len(bundles) != 2 {
		t.Fatalf("bundles: got %d, want 2", len(bundles))
	}
	if bundles[0].Dir != "alpha" || bundles[1].Dir != "bravo" {
		t.Errorf("sort: got %s, %s", bundles[0].Dir, bundles[1].Dir)
	}
}

func TestScanSkillsDir_Empty(t *testing.T) {
	dir := writeSkillsDir(t, nil)
	bundles, err := scanSkillsDir(dir)
	if err != nil {
		t.Fatalf("scanSkillsDir: %v", err)
	}
	if bundles != nil {
		t.Errorf("expected nil for missing skills/, got %d", len(bundles))
	}
}

func TestInjectMcpTool_AddsWhenAbsent(t *testing.T) {
	managed := &agent_yaml.PromptAgent{}
	injectMcpTool(managed, "toolbox-a", "https://proj/mcp", "toolbox-a-toolbox")

	if len(managed.Tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(managed.Tools))
	}
	tool := managed.Tools[0].(map[string]any)
	if tool["type"] != "mcp" || tool["server_url"] != "https://proj/mcp" {
		t.Errorf("tool: got %+v", tool)
	}
	if tool["project_connection_id"] != "toolbox-a-toolbox" {
		t.Errorf("project_connection_id: got %v, want toolbox-a-toolbox", tool["project_connection_id"])
	}
}

func TestInjectMcpTool_NotDuplicated(t *testing.T) {
	managed := &agent_yaml.PromptAgent{
		Tools: []any{
			map[string]any{"type": "mcp", "server_url": "https://proj/mcp"},
		},
	}
	injectMcpTool(managed, "toolbox-a", "https://proj/mcp", "toolbox-a-toolbox")
	if len(managed.Tools) != 1 {
		t.Errorf("expected no duplicate mcp tool, got %d", len(managed.Tools))
	}
	tool := managed.Tools[0].(map[string]any)
	if tool["project_connection_id"] != "toolbox-a-toolbox" {
		t.Errorf("expected connection id backfilled, got %v", tool["project_connection_id"])
	}
}

// TestToolboxNode_SkillsDoNotCreateAToolbox pins the boundary the harness spec
// draws: every harnessed agent already has a service-owned system toolbox, so a
// skills/ folder must never cause azd to build one of its own. Only an explicit
// toolbox: reference reaches this node now.
func TestToolboxNode_SkillsDoNotCreateAToolbox(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}

	node := toolboxNode(g, nil, func() (toolboxBuilder, error) {
		t.Fatal("builder must not be constructed without a toolbox reference")
		return nil, nil
	})
	if node != nil {
		t.Fatal("expected nil node when no toolbox reference is declared")
	}
}

func TestToolboxNode_ReferenceExisting(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeToolboxBuilder{connName: "agent-toolbox"}

	ref := &agent_yaml.ToolboxReference{Name: "existing-tb", Version: "2"}
	node := toolboxNode(g, ref, func() (toolboxBuilder, error) { return fake, nil })
	if node == nil {
		t.Fatal("expected a toolbox node")
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if fake.resolveCalls != 1 {
		t.Errorf("expected 1 resolve, got %d", fake.resolveCalls)
	}
	if fake.lastRef.Name != "existing-tb" || fake.lastRef.Version != "2" {
		t.Errorf("ref: got %+v", fake.lastRef)
	}
	if g.bindings[toolboxMcpURLBindingKey] == nil {
		t.Error("expected toolbox_mcp_url binding")
	}
	if len(managed.Tools) != 1 {
		t.Fatalf("expected mcp tool attached, got %+v", managed.Tools)
	}
	tool := managed.Tools[0].(map[string]any)
	if tool["type"] != "mcp" || tool["project_connection_id"] != "agent-toolbox" {
		t.Errorf("tool: got %+v", tool)
	}
}

func TestToolboxNode_NoneReturnsNil(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.PromptAgent{}, bindings: map[string]any{}}
	node := toolboxNode(g, nil, func() (toolboxBuilder, error) { return nil, nil })
	if node != nil {
		t.Fatal("expected nil node when no reference")
	}
}

// fakeSkillAttacher records the bundles it was given and returns their names.
type fakeSkillAttacher struct {
	attachCalls int
	lastSkills  []skillBundle
	names       []string
	err         error
}

func (a *fakeSkillAttacher) AttachSkills(_ context.Context, skills []skillBundle) ([]string, error) {
	a.attachCalls++
	a.lastSkills = skills
	if a.err != nil {
		return nil, a.err
	}
	if a.names != nil {
		return a.names, nil
	}
	names := make([]string, 0, len(skills))
	for _, s := range skills {
		names = append(names, s.Meta.Name)
	}
	return names, nil
}

func TestSkillsShellNode_NoneReturnsNil(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.PromptAgent{}, bindings: map[string]any{}}
	node := skillsShellNode(g, nil, nil, func() (skillAttacher, error) { return nil, nil })
	if node != nil {
		t.Fatal("expected nil node when no skills and no reference")
	}
}

// fakeHarnessSkillPublisher records the bundles it was given and echoes them
// back as published skills at a fixed version.
type fakeHarnessSkillPublisher struct {
	calls      int
	lastSkills []skillBundle
	published  []publishedSkill
	err        error
}

func (p *fakeHarnessSkillPublisher) PublishSkills(
	_ context.Context, skills []skillBundle,
) ([]publishedSkill, error) {
	p.calls++
	p.lastSkills = skills
	if p.err != nil {
		return nil, p.err
	}
	if p.published != nil {
		return p.published, nil
	}
	out := make([]publishedSkill, 0, len(skills))
	for _, s := range skills {
		out = append(out, publishedSkill{Name: s.Meta.Name, Version: "7"})
	}
	return out, nil
}

func TestSkillsHarnessNode_NoneReturnsNil(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.PromptAgent{}, bindings: map[string]any{}}
	node := skillsHarnessNode(g, nil, func() (harnessSkillPublisher, error) { return nil, nil })
	if node != nil {
		t.Fatal("expected nil node when there are no skills")
	}
}

// TestSkillsHarnessNode_PinsVersionsAndAttachesNoTool is the core of the
// harnessed skills contract: skills land on the harness as versioned
// references, and nothing is added to tools. A skill is not a tool, and the
// toolbox that used to carry them is service-owned.
func TestSkillsHarnessNode_PinsVersionsAndAttachesNoTool(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: "github-copilot"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	pub := &fakeHarnessSkillPublisher{}

	skills := []skillBundle{
		{Dir: "skill-a", Meta: skillMeta{Name: "skill-a", Description: "d", Instructions: "body"}},
		{Dir: "skill-b", Meta: skillMeta{Name: "skill-b", Description: "d", Instructions: "body"}},
	}
	node := skillsHarnessNode(g, skills, func() (harnessSkillPublisher, error) { return pub, nil })
	if node == nil {
		t.Fatal("expected a skills node")
	}
	if node.Kind != nodeSkill {
		t.Errorf("kind: got %q, want %q", node.Kind, nodeSkill)
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if pub.calls != 1 {
		t.Errorf("expected 1 publish call, got %d", pub.calls)
	}
	want := []agent_yaml.HarnessSkillRef{
		{Name: "skill-a", Version: "7"},
		{Name: "skill-b", Version: "7"},
	}
	if !slices.Equal(managed.HarnessSkills, want) {
		t.Errorf("harness skills: got %+v, want %+v", managed.HarnessSkills, want)
	}
	if len(managed.Tools) != 0 {
		t.Errorf("a skill must not become a tool, got %+v", managed.Tools)
	}
	if len(managed.Skills) != 0 {
		t.Errorf("harnessed skills must not land on the definition-level field, got %+v", managed.Skills)
	}
}

// TestSkillsHarnessNode_PinsVersionEvenWhenUnpinned guards the workaround for
// the service returning 500 for a reference with no version: azd always sends
// the version it just published, whether or not SKILL.md pinned one.
func TestSkillsHarnessNode_PinsVersionEvenWhenUnpinned(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: "github-copilot"}
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	pub := &fakeHarnessSkillPublisher{
		published: []publishedSkill{{Name: "skill-a", Version: "3", Pinned: false}},
	}

	skills := []skillBundle{{Dir: "skill-a", Meta: skillMeta{
		Name: "skill-a", Description: "d", Instructions: "body",
	}}}
	node := skillsHarnessNode(g, skills, func() (harnessSkillPublisher, error) { return pub, nil })
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(managed.HarnessSkills) != 1 || managed.HarnessSkills[0].Version != "3" {
		t.Errorf("expected the published version pinned, got %+v", managed.HarnessSkills)
	}
}

func TestSkillsHarnessNode_ResolveIsIdempotent(t *testing.T) {
	managed := &agent_yaml.PromptAgent{
		Model:         "m",
		Instructions:  "i",
		Harness:       "github-copilot",
		HarnessSkills: []agent_yaml.HarnessSkillRef{{Name: "skill-a", Version: "7"}},
	}
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	pub := &fakeHarnessSkillPublisher{}

	skills := []skillBundle{{Dir: "skill-a", Meta: skillMeta{
		Name: "skill-a", Description: "d", Instructions: "body",
	}}}
	node := skillsHarnessNode(g, skills, func() (harnessSkillPublisher, error) { return pub, nil })
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(managed.HarnessSkills) != 1 {
		t.Errorf("expected no duplicate reference, got %+v", managed.HarnessSkills)
	}
}

func TestSkillsHarnessNode_RejectsEmptyInstructions(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: "github-copilot"}
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	pub := &fakeHarnessSkillPublisher{}

	skills := []skillBundle{{Dir: "empty", Meta: skillMeta{Name: "empty", Description: "d"}}}
	node := skillsHarnessNode(g, skills, func() (harnessSkillPublisher, error) { return pub, nil })

	err := node.Validate()
	if err == nil {
		t.Fatal("expected a skill with no instructions to be rejected")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should name the skill, got: %v", err)
	}
	if pub.calls != 0 {
		t.Errorf("validation failure must not publish skills, got %d calls", pub.calls)
	}
}

func TestSkillsHarnessNode_PublisherErrorPropagates(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: "github-copilot"}
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	pub := &fakeHarnessSkillPublisher{err: errors.New("boom")}

	skills := []skillBundle{{Dir: "skill-a", Meta: skillMeta{
		Name: "skill-a", Description: "d", Instructions: "body",
	}}}
	node := skillsHarnessNode(g, skills, func() (harnessSkillPublisher, error) { return pub, nil })

	if err := node.Resolve(context.Background()); err == nil {
		t.Fatal("expected the publish error to propagate")
	}
	if len(managed.HarnessSkills) != 0 {
		t.Errorf("failed publish must leave the definition untouched, got %+v", managed.HarnessSkills)
	}
}

// TestSkillsShellNode_RejectsToolboxReference pins the mutual exclusion between
// this node and toolboxNode: a toolbox is only reachable from inside a harness
// sandbox, so accepting toolbox: here would publish an agent whose skills can
// never run.
func TestSkillsShellNode_RejectsToolboxReference(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeSkillAttacher{}

	ref := &agent_yaml.ToolboxReference{Name: "existing-tb", Version: "2"}
	node := skillsShellNode(g, nil, ref, func() (skillAttacher, error) { return fake, nil })
	if node == nil {
		t.Fatal("expected a skills node")
	}

	err := node.Validate()
	if err == nil {
		t.Fatal("expected toolbox: to be rejected without a harness")
	}
	if !strings.Contains(err.Error(), "harness") {
		t.Errorf("error should point at the harness requirement, got: %v", err)
	}
	if fake.attachCalls != 0 {
		t.Errorf("validation failure must not publish skills, got %d calls", fake.attachCalls)
	}
}

// TestSkillsShellNode_RejectsEmptyInstructions covers a SKILL.md whose body is
// blank: the bundle would publish, but the agent would have no instructions
// telling it what the skill does.
func TestSkillsShellNode_RejectsEmptyInstructions(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}

	skills := []skillBundle{{Dir: "empty", Meta: skillMeta{Name: "empty", Description: "d"}}}
	node := skillsShellNode(g, skills, nil, func() (skillAttacher, error) {
		return &fakeSkillAttacher{}, nil
	})

	err := node.Validate()
	if err == nil {
		t.Fatal("expected a skill with no instructions to be rejected")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should name the offending skill, got: %v", err)
	}
}

// TestSkillsShellNode_PublishesAndInjectsShell asserts the node's whole job:
// publish the bundles, reference the returned names on the definition, and add
// the shell tool that makes them runnable.
func TestSkillsShellNode_PublishesAndInjectsShell(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeSkillAttacher{}

	skills := []skillBundle{
		{Dir: "a", Meta: skillMeta{Name: "skill-a", Description: "d", Instructions: "do a"}},
		{Dir: "b", Meta: skillMeta{Name: "skill-b", Description: "d", Instructions: "do b"}},
	}
	node := skillsShellNode(g, skills, nil, func() (skillAttacher, error) { return fake, nil })
	if node == nil {
		t.Fatal("expected a skills node")
	}
	if node.Kind != nodeSkill {
		t.Errorf("kind: got %v, want %v", node.Kind, nodeSkill)
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if fake.attachCalls != 1 {
		t.Errorf("expected 1 attach call, got %d", fake.attachCalls)
	}
	if len(fake.lastSkills) != 2 {
		t.Errorf("expected both bundles published, got %d", len(fake.lastSkills))
	}
	if want := []string{"skill-a", "skill-b"}; !slices.Equal(managed.Skills, want) {
		t.Errorf("skills: got %v, want %v", managed.Skills, want)
	}
	if len(managed.Tools) != 1 {
		t.Fatalf("expected the shell tool to be injected, got %+v", managed.Tools)
	}
	if got := managed.Tools[0].(map[string]any)["type"]; got != promptSkillShellToolType {
		t.Errorf("tool type: got %v, want %v", got, promptSkillShellToolType)
	}
}

// TestSkillsShellNode_ResolveIsIdempotent covers a re-run of the deploy graph:
// neither the skill names nor the shell tool may be duplicated, since both are
// sent verbatim to the API.
func TestSkillsShellNode_ResolveIsIdempotent(t *testing.T) {
	managed := &agent_yaml.PromptAgent{
		Model:        "m",
		Instructions: "i",
		Skills:       []string{"skill-a"},
		Tools:        []any{map[string]any{"type": promptSkillShellToolType}},
	}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeSkillAttacher{}

	skills := []skillBundle{
		{Dir: "a", Meta: skillMeta{Name: "skill-a", Description: "d", Instructions: "do a"}},
	}
	node := skillsShellNode(g, skills, nil, func() (skillAttacher, error) { return fake, nil })
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if want := []string{"skill-a"}; !slices.Equal(managed.Skills, want) {
		t.Errorf("skills: got %v, want %v", managed.Skills, want)
	}
	if len(managed.Tools) != 1 {
		t.Errorf("expected the existing shell tool to be reused, got %+v", managed.Tools)
	}
}

// TestSkillsShellNode_AttacherErrorPropagates asserts a publish failure fails
// the deploy rather than leaving the definition half-wired -- an agent that
// references skills the service never received.
func TestSkillsShellNode_AttacherErrorPropagates(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeSkillAttacher{err: errors.New("publish failed")}

	skills := []skillBundle{
		{Dir: "a", Meta: skillMeta{Name: "skill-a", Description: "d", Instructions: "do a"}},
	}
	node := skillsShellNode(g, skills, nil, func() (skillAttacher, error) { return fake, nil })

	err := node.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected the attacher error to propagate")
	}
	if len(managed.Skills) != 0 || len(managed.Tools) != 0 {
		t.Errorf("definition must be left untouched on failure: skills=%v tools=%v",
			managed.Skills, managed.Tools)
	}
}
