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

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// testPromptHarness returns a minimal harness block. Each caller gets its own
// value because the deploy graph writes skills back onto the agent.
func testPromptHarness() *agent_yaml.PromptHarness {
	return agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessGitHubCopilot)
}

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

// TestToolboxNode_PrefersSiblingEndpoint verifies the toolbox node hands the
// builder the MCP url the sibling azure.ai.toolbox service published, rather
// than letting the builder synthesize one from the name. The toolboxes extension
// owns the toolbox's lifecycle and knows the endpoint it actually created, so
// the two extensions must not be free to disagree about where it lives.
func TestToolboxNode_PrefersSiblingEndpoint(t *testing.T) {
	published := "https://acct.services.ai.azure.com/toolboxes/tb/mcp"
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	g := &promptGraph{managed: managed, bindings: map[string]any{}, env: map[string]string{
		envkey.ToolboxMCPEndpoint("tb"):     published,
		envkey.ToolboxProjectEndpoint("tb"): "https://acct.services.ai.azure.com/api/projects/p",
		"FOUNDRY_PROJECT_ENDPOINT":          "https://acct.services.ai.azure.com/api/projects/p",
	}}

	builder := &fakeToolboxBuilder{}
	node := toolboxNode(g, &agent_yaml.ToolboxReference{Name: "tb", Connection: "tb-conn"}, func() (toolboxBuilder, error) {
		return builder, nil
	})
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if builder.lastRef.MCPEndpoint != published {
		t.Errorf("published endpoint: got %q, want %q", builder.lastRef.MCPEndpoint, published)
	}
}

// TestToolboxNode_RejectsCrossProjectEndpoint verifies a marker left over from a
// different Foundry project fails the deploy instead of pointing the agent at a
// toolbox it cannot reach, which the service would only report at run time.
func TestToolboxNode_RejectsCrossProjectEndpoint(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	g := &promptGraph{managed: managed, bindings: map[string]any{}, env: map[string]string{
		envkey.ToolboxMCPEndpoint("tb"):     "https://other.services.ai.azure.com/toolboxes/tb/mcp",
		envkey.ToolboxProjectEndpoint("tb"): "https://other.services.ai.azure.com/api/projects/q",
		"FOUNDRY_PROJECT_ENDPOINT":          "https://acct.services.ai.azure.com/api/projects/p",
	}}

	node := toolboxNode(g, &agent_yaml.ToolboxReference{Name: "tb", Connection: "tb-conn"}, func() (toolboxBuilder, error) {
		return &fakeToolboxBuilder{}, nil
	})
	if err := node.Resolve(context.Background()); err == nil {
		t.Fatal("expected a toolbox published to another project to fail the deploy")
	}
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

	ref := &agent_yaml.ToolboxReference{Name: "existing-tb", Version: "2", Connection: "agent-toolbox"}
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

// skillMarkers builds the azd environment a deployed sibling azure.ai.skill
// service leaves behind: one SKILL_<NAME>_VERSION entry per published skill.
func skillMarkers(nameToVersion map[string]string) map[string]string {
	env := map[string]string{}
	for name, version := range nameToVersion {
		env[envkey.SkillVersion(name)] = version
	}
	return env
}

func TestSkillsShellNode_NoneReturnsNil(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.PromptAgent{}, bindings: map[string]any{}}
	node := skillsShellNode(g, nil, nil)
	if node != nil {
		t.Fatal("expected nil node when no skills and no reference")
	}
}

func TestSkillsHarnessNode_NoneReturnsNil(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.PromptAgent{}, bindings: map[string]any{}}
	node := skillsHarnessNode(g, nil)
	if node != nil {
		t.Fatal("expected nil node when there are no skills")
	}
}

// TestSkillsHarnessNode_PinsVersionsAndAttachesNoTool is the core of the
// harnessed skills contract: skills land on the harness as versioned
// references taken from the sibling skill services' markers, and nothing is
// added to tools. A skill is not a tool, and the toolbox that used to carry
// them is service-owned.
func TestSkillsHarnessNode_PinsVersionsAndAttachesNoTool(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: testPromptHarness()}
	managed.Name = "agent"
	g := &promptGraph{
		managed:  managed,
		bindings: map[string]any{},
		env:      skillMarkers(map[string]string{"skill-a": "7", "skill-b": "7"}),
	}

	skills := []skillBundle{
		{Dir: "skill-a", Meta: skillMeta{Name: "skill-a", Description: "d", Instructions: "body"}},
		{Dir: "skill-b", Meta: skillMeta{Name: "skill-b", Description: "d", Instructions: "body"}},
	}
	node := skillsHarnessNode(g, skills)
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

	want := []agent_yaml.HarnessSkillRef{
		{Name: "skill-a", Version: "7"},
		{Name: "skill-b", Version: "7"},
	}
	if !slices.Equal(managed.Harness.Skills, want) {
		t.Errorf("harness skills: got %+v, want %+v", managed.Harness.Skills, want)
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
// the version the skill service published, whether or not SKILL.md pinned one.
func TestSkillsHarnessNode_PinsVersionEvenWhenUnpinned(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: testPromptHarness()}
	g := &promptGraph{
		managed:  managed,
		bindings: map[string]any{},
		env:      skillMarkers(map[string]string{"skill-a": "3"}),
	}

	skills := []skillBundle{{Dir: "skill-a", Meta: skillMeta{
		Name: "skill-a", Description: "d", Instructions: "body",
	}}}
	node := skillsHarnessNode(g, skills)
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(managed.Harness.Skills) != 1 || managed.Harness.Skills[0].Version != "3" {
		t.Errorf("expected the published version pinned, got %+v", managed.Harness.Skills)
	}
}

func TestSkillsHarnessNode_ResolveIsIdempotent(t *testing.T) {
	managed := &agent_yaml.PromptAgent{
		Model:        "m",
		Instructions: "i",
		Harness:      testPromptHarness(),
	}
	managed.Harness.Skills = []agent_yaml.HarnessSkillRef{{Name: "skill-a", Version: "7"}}
	g := &promptGraph{
		managed:  managed,
		bindings: map[string]any{},
		env:      skillMarkers(map[string]string{"skill-a": "7"}),
	}

	skills := []skillBundle{{Dir: "skill-a", Meta: skillMeta{
		Name: "skill-a", Description: "d", Instructions: "body",
	}}}
	node := skillsHarnessNode(g, skills)
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(managed.Harness.Skills) != 1 {
		t.Errorf("expected no duplicate reference, got %+v", managed.Harness.Skills)
	}
}

func TestSkillsHarnessNode_RejectsEmptyInstructions(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: testPromptHarness()}
	g := &promptGraph{managed: managed, bindings: map[string]any{}}

	skills := []skillBundle{{Dir: "empty", Meta: skillMeta{Name: "empty", Description: "d"}}}
	node := skillsHarnessNode(g, skills)

	err := node.Validate()
	if err == nil {
		t.Fatal("expected a skill with no instructions to be rejected")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should name the skill, got: %v", err)
	}
}

// TestSkillsHarnessNode_MissingMarkerFails covers the case the replacement of
// azd's own publisher introduces: a skills/ folder with no sibling
// azure.ai.skill service, so nothing ever created the skill. The deploy must
// fail with the azure.yaml entry to add rather than silently drop the skill.
func TestSkillsHarnessNode_MissingMarkerFails(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: testPromptHarness()}
	g := &promptGraph{managed: managed, bindings: map[string]any{}, env: map[string]string{}}

	skills := []skillBundle{{Dir: "skill-a", Meta: skillMeta{
		Name: "skill-a", Description: "d", Instructions: "body",
	}}}
	node := skillsHarnessNode(g, skills)

	err := node.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected an unpublished skill to fail the deploy")
	}
	if !strings.Contains(err.Error(), "SKILL_SKILL_A_VERSION") {
		t.Errorf("error should name the missing marker, got: %v", err)
	}
	svcErr, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected a structured error, got %T", err)
	}
	if !strings.Contains(svcErr.Suggestion, "azure.ai.skill") {
		t.Errorf("suggestion should name the host to declare, got: %v", svcErr.Suggestion)
	}
	if len(managed.Harness.Skills) != 0 {
		t.Errorf("failed resolve must leave the definition untouched, got %+v", managed.Harness.Skills)
	}
}

// TestResolveSkillMarkers_RejectsCrossProjectVersion covers a stale marker left
// by a deploy against a different Foundry project: the version id would not
// resolve there, and the service reports that only at run time.
func TestResolveSkillMarkers_RejectsCrossProjectVersion(t *testing.T) {
	env := skillMarkers(map[string]string{"skill-a": "7"})
	env[envkey.SkillProjectEndpoint("skill-a")] = "https://other.services.ai.azure.com/api/projects/other"
	env["FOUNDRY_PROJECT_ENDPOINT"] = "https://mine.services.ai.azure.com/api/projects/mine"

	skills := []skillBundle{{Dir: "skill-a", Meta: skillMeta{Name: "skill-a"}}}
	if _, err := resolveSkillMarkers(skills, env["FOUNDRY_PROJECT_ENDPOINT"], env); err == nil {
		t.Fatal("expected a marker from another project to be rejected")
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

	ref := &agent_yaml.ToolboxReference{Name: "existing-tb", Version: "2", Connection: "agent-toolbox"}
	node := skillsShellNode(g, nil, ref)
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
}

// TestSkillsShellNode_RejectsEmptyInstructions covers a SKILL.md whose body is
// blank: the bundle would publish, but the agent would have no instructions
// telling it what the skill does.
func TestSkillsShellNode_RejectsEmptyInstructions(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}

	skills := []skillBundle{{Dir: "empty", Meta: skillMeta{Name: "empty", Description: "d"}}}
	node := skillsShellNode(g, skills, nil)

	err := node.Validate()
	if err == nil {
		t.Fatal("expected a skill with no instructions to be rejected")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should name the offending skill, got: %v", err)
	}
}

// TestSkillsShellNode_AttachesAndInjectsShell asserts the node's whole job:
// reference the skills the sibling services published, and add the shell tool
// that makes them runnable.
func TestSkillsShellNode_AttachesAndInjectsShell(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{
		managed:  managed,
		bindings: map[string]any{},
		env:      skillMarkers(map[string]string{"skill-a": "1", "skill-b": "1"}),
	}

	skills := []skillBundle{
		{Dir: "a", Meta: skillMeta{Name: "skill-a", Description: "d", Instructions: "do a"}},
		{Dir: "b", Meta: skillMeta{Name: "skill-b", Description: "d", Instructions: "do b"}},
	}
	node := skillsShellNode(g, skills, nil)
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
	g := &promptGraph{
		managed:  managed,
		bindings: map[string]any{},
		env:      skillMarkers(map[string]string{"skill-a": "1"}),
	}

	skills := []skillBundle{
		{Dir: "a", Meta: skillMeta{Name: "skill-a", Description: "d", Instructions: "do a"}},
	}
	node := skillsShellNode(g, skills, nil)
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

// TestSkillsShellNode_MissingMarkerFails asserts an unpublished skill fails the
// deploy rather than leaving the definition half-wired -- an agent that
// references skills the service never received.
func TestSkillsShellNode_MissingMarkerFails(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}, env: map[string]string{}}

	skills := []skillBundle{
		{Dir: "a", Meta: skillMeta{Name: "skill-a", Description: "d", Instructions: "do a"}},
	}
	node := skillsShellNode(g, skills, nil)

	err := node.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected an unpublished skill to fail the deploy")
	}
	if len(managed.Skills) != 0 || len(managed.Tools) != 0 {
		t.Errorf("definition must be left untouched on failure: skills=%v tools=%v",
			managed.Skills, managed.Tools)
	}
}
