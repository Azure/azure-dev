// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/envkey"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

// testMicrosoftCopilotHarness returns a minimal harness block. Each caller gets its own
// value because the deploy graph writes skills back onto the agent.
func testMicrosoftCopilotHarness() *agent_yaml.PromptHarness {
	return agent_yaml.NewPromptHarness(agent_api.ManagedAgentHarnessMicrosoftCopilot)
}

// fakeToolboxBuilder records calls and returns a fixed MCP url.
type fakeToolboxBuilder struct {
	mcpURL              string
	projectConnectionID string
	resolveCalls        int
	lastRef             toolboxRef
}

func (b *fakeToolboxBuilder) ResolveToolbox(_ context.Context, ref toolboxRef) (toolboxAttachment, error) {
	b.resolveCalls++
	b.lastRef = ref
	if b.mcpURL == "" {
		b.mcpURL = "https://proj/toolboxes/existing/versions/2/mcp"
	}
	return toolboxAttachment{McpURL: b.mcpURL, ProjectConnectionID: b.projectConnectionID}, nil
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
	node := toolboxNode(g, []agent_yaml.ToolboxReference{
		{Name: "tb"},
	}, func() (toolboxBuilder, error) {
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

	node := toolboxNode(g, []agent_yaml.ToolboxReference{
		{Name: "tb"},
	}, func() (toolboxBuilder, error) {
		return &fakeToolboxBuilder{}, nil
	})
	if err := node.Resolve(context.Background()); err == nil {
		t.Fatal("expected a toolbox published to another project to fail the deploy")
	}
}

func TestToolboxNode_UsesServiceKey(t *testing.T) {
	t.Parallel()
	const projectEndpoint = "https://example.test/api/projects/current"
	const mcpEndpoint = projectEndpoint + "/toolboxes/my-tools/mcp"
	for _, sameProject := range []bool{true, false} {
		t.Run(map[bool]string{true: "same project", false: "different project"}[sameProject], func(t *testing.T) {
			t.Parallel()
			markerProject := projectEndpoint
			if !sameProject {
				markerProject = "https://example.test/api/projects/other"
			}
			g := &promptGraph{
				managed:  &agent_yaml.PromptAgent{Model: "m", Instructions: "i"},
				bindings: map[string]any{},
				env: map[string]string{
					"FOUNDRY_PROJECT_ENDPOINT":               projectEndpoint,
					envkey.ToolboxMCPEndpoint("MyTools"):     mcpEndpoint,
					envkey.ToolboxProjectEndpoint("MyTools"): markerProject,
				},
			}
			builder := &fakeToolboxBuilder{}
			node := toolboxNode(g, []agent_yaml.ToolboxReference{{Name: "MyTools"}},
				func() (toolboxBuilder, error) { return builder, nil })
			err := node.Resolve(t.Context())
			if !sameProject {
				require.ErrorContains(t, err, "different Foundry project")
				require.Zero(t, builder.resolveCalls)
				return
			}
			require.NoError(t, err)
			require.Equal(t, mcpEndpoint, builder.lastRef.MCPEndpoint)
			require.Equal(t, "MyTools", builder.lastRef.Name)
		})
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
	fake := &fakeToolboxBuilder{projectConnectionID: "agent-toolbox"}

	refs := []agent_yaml.ToolboxReference{
		{Name: "existing-tb"},
	}
	node := toolboxNode(g, refs, func() (toolboxBuilder, error) { return fake, nil })
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
	if fake.lastRef.Name != "existing-tb" {
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

func TestToolboxNode_WithoutProjectConnectionID(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeToolboxBuilder{}

	node := toolboxNode(g, []agent_yaml.ToolboxReference{{Name: "public-tools"}}, func() (toolboxBuilder, error) {
		return fake, nil
	})
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(t.Context()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	tool := managed.Tools[0].(map[string]any)
	if _, exists := tool["project_connection_id"]; exists {
		t.Errorf("project_connection_id should be omitted: %+v", tool)
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

// TestSkillsHarnessNode_PinsVersionsAndAttachesNoTool verifies resolved skills
// are stored as versioned top-level references for request mapping.
func TestSkillsHarnessNode_PinsVersionsAndAttachesNoTool(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: testMicrosoftCopilotHarness()}
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
	if !slices.Equal(managed.ResolvedSkills, want) {
		t.Errorf("resolved skills: got %+v, want %+v", managed.ResolvedSkills, want)
	}
	if len(managed.Tools) != 0 {
		t.Errorf("a skill must not become a tool, got %+v", managed.Tools)
	}
}

// TestSkillsHarnessNode_PinsVersionEvenWhenUnpinned guards the workaround for
// the service returning 500 for a reference with no version: azd always sends
// the version the skill service published, whether or not SKILL.md pinned one.
func TestSkillsHarnessNode_PinsVersionEvenWhenUnpinned(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: testMicrosoftCopilotHarness()}
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

	if len(managed.ResolvedSkills) != 1 || managed.ResolvedSkills[0].Version != "3" {
		t.Errorf("expected the published version pinned, got %+v", managed.ResolvedSkills)
	}
}

func TestSkillsHarnessNode_ResolveIsIdempotent(t *testing.T) {
	managed := &agent_yaml.PromptAgent{
		Model:        "m",
		Instructions: "i",
		Harness:      testMicrosoftCopilotHarness(),
		ResolvedSkills: []agent_yaml.HarnessSkillRef{
			{Name: "skill-a", Version: "7"},
		},
	}
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

	if len(managed.ResolvedSkills) != 1 {
		t.Errorf("expected no duplicate reference, got %+v", managed.ResolvedSkills)
	}
}

func TestSkillsHarnessNode_RejectsEmptyInstructions(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: testMicrosoftCopilotHarness()}
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
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i", Harness: testMicrosoftCopilotHarness()}
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
	if len(managed.ResolvedSkills) != 0 {
		t.Errorf("failed resolve must leave the definition untouched, got %+v", managed.ResolvedSkills)
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

func TestResolveSkillMarkersUsesSanitizedServiceName(t *testing.T) {
	t.Parallel()

	env := skillMarkers(map[string]string{"codereview": "7"})
	skills := []skillBundle{{Dir: "code-review", Meta: skillMeta{Name: "code review"}}}
	resolved, err := resolveSkillMarkers(skills, "", env)
	require.NoError(t, err)
	require.Equal(t, []resolvedSkill{{Name: "codereview", Version: "7"}}, resolved)
}

// TestSkillsShellNode_RejectsToolboxReference pins the mutual exclusion between
// this node and toolboxNode: a toolbox is only reachable from inside a harness
// sandbox, so accepting toolbox: here would publish an agent whose skills can
// never run.
func TestSkillsShellNode_RejectsToolboxReference(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "m", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}

	refs := []agent_yaml.ToolboxReference{
		{Name: "existing-tb"},
	}
	node := skillsShellNode(g, nil, refs)
	if node == nil {
		t.Fatal("expected a skills node")
	}

	err := node.Validate()
	if err == nil {
		t.Fatal("expected toolboxes: to be rejected without a harness")
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

// TestSkillsShellNode_AttachesVersionedReferences asserts the node resolves the
// exact skill versions published by sibling services.
func TestSkillsShellNode_AttachesVersionedReferences(t *testing.T) {
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

	want := []agent_yaml.HarnessSkillRef{{Name: "skill-a", Version: "1"}, {Name: "skill-b", Version: "1"}}
	if !slices.Equal(managed.ResolvedSkills, want) {
		t.Errorf("resolved skills: got %v, want %v", managed.ResolvedSkills, want)
	}
	if len(managed.Tools) != 0 {
		t.Errorf("skills must not synthesize tools, got %+v", managed.Tools)
	}
}

// TestSkillsShellNode_ResolveIsIdempotent covers a re-run of the deploy graph:
// neither the skill names nor the shell tool may be duplicated, since both are
// sent verbatim to the API.
func TestSkillsShellNode_ResolveIsIdempotent(t *testing.T) {
	managed := &agent_yaml.PromptAgent{
		Model:        "m",
		Instructions: "i",
		ResolvedSkills: []agent_yaml.HarnessSkillRef{
			{Name: "skill-a", Version: "1"},
		},
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

	if len(managed.ResolvedSkills) != 1 {
		t.Errorf("expected the existing reference to be reused, got %+v", managed.ResolvedSkills)
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
	if len(managed.ResolvedSkills) != 0 || len(managed.Tools) != 0 {
		t.Errorf("definition must be left untouched on failure: skills=%v tools=%v",
			managed.ResolvedSkills, managed.Tools)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestFoundryToolboxBuilder_CreatesAndReusesUserEntraTokenConnection(t *testing.T) {
	t.Parallel()

	const projectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const mcpURLv1 = projectEndpoint + "/toolboxes/research-tools/versions/1/mcp?api-version=v1"
	const mcpURLv2 = projectEndpoint + "/toolboxes/research-tools/versions/2/mcp?api-version=v1"

	stored := map[string]*toolboxConnectionBody{}
	var getCalls, putCalls int
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
			connName := parts[len(parts)-1]
			switch req.Method {
			case http.MethodGet:
				getCalls++
				existing := stored[connName]
				if existing == nil {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Request:    req,
						Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"NotFound"}}`)),
					}, nil
				}
				raw, _ := json.Marshal(existing)
				return &http.Response{
					StatusCode: http.StatusOK,
					Request:    req,
					Body:       io.NopCloser(bytes.NewReader(raw)),
				}, nil
			case http.MethodPut:
				putCalls++
				var body toolboxConnectionBody
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					return nil, err
				}
				stored[connName] = &body
				raw, _ := json.Marshal(body)
				return &http.Response{
					StatusCode: http.StatusOK,
					Request:    req,
					Body:       io.NopCloser(bytes.NewReader(raw)),
				}, nil
			default:
				t.Fatalf("unexpected method %s", req.Method)
				return nil, nil
			}
		}),
	})

	builder := &foundryToolboxBuilder{
		subscriptionID: "sub-1",
		resourceGroup:  "rg-1",
		accountName:    "acct",
		projectName:    "proj",
		pipeline:       pipeline,
		explicitConns:  map[string]struct{}{},
	}

	// 1. First deploy: 404 -> creates connection via PUT.
	att1, err := builder.ResolveToolbox(t.Context(), toolboxRef{
		Name:        "research-tools",
		MCPEndpoint: mcpURLv1,
	})
	require.NoError(t, err)
	require.Equal(t, 1, getCalls)
	require.Equal(t, 1, putCalls)
	require.Equal(t, "research-tools", att1.ServerLabel)
	require.Equal(t, mcpURLv1, att1.McpURL)
	require.Equal(t, "research-tools-mcp", att1.ProjectConnectionID)
	require.NotNil(t, stored["research-tools-mcp"])
	require.Equal(t, "RemoteTool", stored["research-tools-mcp"].Properties.Category)
	require.Equal(t, "UserEntraToken", stored["research-tools-mcp"].Properties.AuthType)
	require.Equal(t, "https://ai.azure.com", stored["research-tools-mcp"].Properties.Audience)
	require.Equal(t, mcpURLv1, stored["research-tools-mcp"].Properties.Target)
	require.Equal(t, map[string]string{
		"type":           "toolbox_MCP",
		"toolboxName":    "research-tools",
		"toolboxVersion": "1",
	}, stored["research-tools-mcp"].Properties.Metadata)

	// 2. Second deploy of same version: 200 OK with exact match -> reuses without PUT.
	att2, err := builder.ResolveToolbox(t.Context(), toolboxRef{
		Name:        "research-tools",
		MCPEndpoint: mcpURLv1,
	})
	require.NoError(t, err)
	require.Equal(t, 2, getCalls)
	require.Equal(t, 1, putCalls, "compatible connection must be reused without redundant PUT")
	require.Equal(t, att1, att2)

	// 3. Toolbox updated to v2: preserves research-tools-mcp (still pointing to v1)
	// and creates version-scoped research-tools-v2-mcp.
	att3, err := builder.ResolveToolbox(t.Context(), toolboxRef{
		Name:        "research-tools",
		MCPEndpoint: mcpURLv2,
	})
	require.NoError(t, err)
	require.Equal(t, 4, getCalls)
	require.Equal(t, 2, putCalls)
	require.Equal(t, mcpURLv2, att3.McpURL)
	require.Equal(t, "research-tools-v2-mcp", att3.ProjectConnectionID)
	require.Equal(t, mcpURLv1, stored["research-tools-mcp"].Properties.Target, "v1 connection must remain intact")
	require.NotNil(t, stored["research-tools-v2-mcp"])
	require.Equal(t, mcpURLv2, stored["research-tools-v2-mcp"].Properties.Target)
	require.Equal(t, "2", stored["research-tools-v2-mcp"].Properties.Metadata["toolboxVersion"])
}

func TestFoundryToolboxBuilder_PutConnectionReplaysBodyOnRetry(t *testing.T) {
	t.Parallel()

	const projectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const mcpURL = projectEndpoint + "/toolboxes/research-tools/versions/1/mcp?api-version=v1"

	putAttempts := 0
	var secondPutBody toolboxConnectionBody
	pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
		Retry: policy.RetryOptions{
			MaxRetries: 1,
			RetryDelay: time.Millisecond,
		},
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Request:    req,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			}
			putAttempts++
			rawBytes, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NotEmpty(t, rawBytes, "PUT body must be non-empty on attempt %d", putAttempts)
			if putAttempts == 1 {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Request:    req,
					Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"ServiceUnavailable"}}`)),
				}, nil
			}
			require.NoError(t, json.Unmarshal(rawBytes, &secondPutBody))
			return &http.Response{
				StatusCode: http.StatusOK,
				Request:    req,
				Body:       io.NopCloser(bytes.NewReader(rawBytes)),
			}, nil
		}),
	})

	builder := &foundryToolboxBuilder{
		subscriptionID: "sub-1",
		resourceGroup:  "rg-1",
		accountName:    "acct",
		projectName:    "proj",
		pipeline:       pipeline,
		explicitConns:  map[string]struct{}{},
	}

	att, err := builder.ResolveToolbox(t.Context(), toolboxRef{
		Name:        "research-tools",
		MCPEndpoint: mcpURL,
	})
	require.NoError(t, err)
	require.Equal(t, 2, putAttempts)
	require.Equal(t, "research-tools-mcp", att.ProjectConnectionID)
	require.Equal(t, mcpURL, secondPutBody.Properties.Target)
}

type fakeTokenCredential struct{}

func (fakeTokenCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestNewFoundryToolboxBuilder_ResolvesAccountFromProjectResourceIDAndChecksOwnership(t *testing.T) {
	t.Parallel()

	const customSubdomainEndpoint = "https://custom-subdomain.services.ai.azure.com/api/projects/my-proj"
	const projectResourceID = "/subscriptions/sub-from-id/resourceGroups/rg-from-id/" +
		"providers/Microsoft.CognitiveServices/accounts/arm-account-name/projects/arm-project-name"

	t.Run("prefers AZURE_AI_PROJECT_ID over custom subdomain and uses ResourceName for ownership", func(t *testing.T) {
		t.Parallel()
		g := &promptGraph{
			env: map[string]string{
				"AZURE_AI_PROJECT_ID": projectResourceID,
			},
			projectServices: map[string]*azdext.ServiceConfig{
				// Service key is "research-tools-mcp", but its ARM ResourceName is "other-conn":
				// it must NOT block "research-tools-mcp".
				"research-tools-mcp": promptConnectionService(t, "research-tools-mcp", "other-conn"),
			},
		}
		settings := &PromptAgentSettings{
			SubscriptionID:  "sub-fallback",
			ResourceGroup:   "rg-fallback",
			ProjectEndpoint: customSubdomainEndpoint,
		}
		rawBuilder, err := newFoundryToolboxBuilder(g, settings, fakeTokenCredential{})
		require.NoError(t, err)
		builder := rawBuilder.(*foundryToolboxBuilder)
		require.Equal(t, "sub-from-id", builder.subscriptionID)
		require.Equal(t, "rg-from-id", builder.resourceGroup)
		require.Equal(t, "arm-account-name", builder.accountName)
		require.Equal(t, "arm-project-name", builder.projectName)
		require.NotContains(t, builder.explicitConns, "research-tools-mcp")
		require.Contains(t, builder.explicitConns, "other-conn")
	})

	t.Run("propagates connection $ref resolution errors", func(t *testing.T) {
		t.Parallel()
		g := &promptGraph{
			projectRoot: t.TempDir(),
			projectServices: map[string]*azdext.ServiceConfig{
				"broken-conn": {
					Name:                 "broken-conn",
					Host:                 foundryConnectionHost,
					AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./missing-connection.yaml"}),
				},
			},
		}
		settings := &PromptAgentSettings{
			SubscriptionID:  "sub-1",
			ResourceGroup:   "rg-1",
			ProjectEndpoint: customSubdomainEndpoint,
		}
		_, err := newFoundryToolboxBuilder(g, settings, fakeTokenCredential{})
		require.Error(t, err)
	})
}

func TestReconcileMcpTool_UpdatesPriorToolboxVersionAndRejectsConflictingURL(t *testing.T) {
	t.Parallel()

	const projectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const urlV1 = projectEndpoint + "/toolboxes/research-tools/versions/1/mcp?api-version=v1"
	const urlV2 = projectEndpoint + "/toolboxes/research-tools/versions/2/mcp?api-version=v1"

	t.Run("updates prior toolbox version URL and connection in place while preserving options", func(t *testing.T) {
		t.Parallel()
		managed := &agent_yaml.PromptAgent{
			Tools: []any{
				map[string]any{
					"type":                  "mcp",
					"server_label":          "research-tools",
					"server_url":            urlV1,
					"project_connection_id": "research-tools-mcp",
					"require_approval":      "never",
					"allowed_tools":         []string{"web_search"},
				},
			},
		}
		require.NoError(t, reconcileMcpTool(managed, "research-tools", urlV2, "research-tools-v2-mcp"))
		require.Len(t, managed.Tools, 1)
		tool := managed.Tools[0].(map[string]any)
		require.Equal(t, urlV2, tool["server_url"])
		require.Equal(t, "research-tools-v2-mcp", tool["project_connection_id"])
		require.Equal(t, "never", tool["require_approval"])
		require.Equal(t, []string{"web_search"}, tool["allowed_tools"])
	})

	t.Run("rejects conflicting non-toolbox URL with same server_label", func(t *testing.T) {
		t.Parallel()
		managed := &agent_yaml.PromptAgent{
			Tools: []any{
				map[string]any{
					"type":         "mcp",
					"server_label": "research-tools",
					"server_url":   "https://external.example.com/mcp",
				},
			},
		}
		err := reconcileMcpTool(managed, "research-tools", urlV1, "research-tools-mcp")
		require.ErrorContains(t, err, `MCP tool "research-tools" declares server_url`)
		require.Len(t, managed.Tools, 1)
	})

	t.Run("rejects cross-project toolbox URL with same toolbox name", func(t *testing.T) {
		t.Parallel()
		managed := &agent_yaml.PromptAgent{
			Tools: []any{
				map[string]any{
					"type":         "mcp",
					"server_label": "research-tools",
					"server_url": "https://other.services.ai.azure.com/api/projects/other/" +
						"toolboxes/research-tools/versions/1/mcp?api-version=v1",
				},
			},
		}
		err := reconcileMcpTool(managed, "research-tools", urlV1, "research-tools-mcp")
		require.ErrorContains(t, err, `MCP tool "research-tools" declares server_url`)
		require.Len(t, managed.Tools, 1)
	})

	t.Run("preserves user-authored server_label and toolChoice on exact URL match", func(t *testing.T) {
		t.Parallel()
		managed := &agent_yaml.PromptAgent{
			Model:        "gpt-4.1-mini",
			Instructions: "Help.",
			ToolChoice:   map[string]any{"type": "mcp", "server_label": "search"},
			Tools: []any{
				map[string]any{
					"type":             "mcp",
					"server_label":     "search",
					"server_url":       urlV1,
					"require_approval": "never",
				},
			},
		}
		require.NoError(t, reconcileMcpTool(managed, "research-tools", urlV1, "research-tools-mcp"))
		require.Len(t, managed.Tools, 1)
		tool := managed.Tools[0].(map[string]any)
		require.Equal(t, "search", tool["server_label"])
		require.Equal(t, urlV1, tool["server_url"])
		require.Equal(t, "research-tools-mcp", tool["project_connection_id"])
		require.Equal(t, "never", tool["require_approval"])
		require.Equal(t, map[string]any{"type": "mcp", "server_label": "search"}, managed.ToolChoice)
	})
}

func TestFoundryToolboxBuilder_RejectsNonFoundryEndpointHost(t *testing.T) {
	t.Parallel()

	armCalled := false
	builder := &foundryToolboxBuilder{
		subscriptionID: "sub-1",
		resourceGroup:  "rg-1",
		accountName:    "acct",
		projectName:    "proj",
		explicitConns:  map[string]struct{}{},
		pipeline: runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				armCalled = true
				return nil, errors.New("unexpected ARM request")
			}),
		}),
	}

	for _, badURL := range []string{
		"https://untrusted.example/api/projects/proj/toolboxes/tools/versions/1/mcp?api-version=v1",
		"https://untrusted.example/toolboxes/tools/versions/1/mcp",
		"http://acct.services.ai.azure.com/api/projects/proj/toolboxes/tools/versions/1/mcp?api-version=v1",
		"https://acct.services.ai.azure.com/toolboxes/tools/versions/1/mcp?api-version=v1",
	} {
		_, err := builder.ResolveToolbox(t.Context(), toolboxRef{
			Name:        "tools",
			MCPEndpoint: badURL,
		})
		require.Error(t, err, badURL)
		require.False(t, armCalled, "must not contact ARM for non-Foundry endpoint %q", badURL)
	}
}

func TestToolboxNode_TwoServicesTargetingDifferentVersionsDisambiguateLabelsAndPreserveOptions(t *testing.T) {
	t.Parallel()

	const projectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const urlV1 = projectEndpoint + "/toolboxes/research-tools/versions/1/mcp?api-version=v1"
	const urlV2 = projectEndpoint + "/toolboxes/research-tools/versions/2/mcp?api-version=v1"

	managed := &agent_yaml.PromptAgent{
		Model:        "gpt-4.1-mini",
		Instructions: "Help.",
		Harness:      testMicrosoftCopilotHarness(),
		Tools: []any{
			map[string]any{
				"type":             "mcp",
				"server_label":     "tools-v1",
				"require_approval": "never",
			},
		},
		Toolboxes: []agent_yaml.ToolboxReference{
			{Name: "tools-v1"},
			{Name: "tools-v2"},
		},
	}
	g := &promptGraph{
		managed:  managed,
		bindings: map[string]any{},
		env: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                projectEndpoint,
			envkey.ToolboxMCPEndpoint("tools-v1"):     urlV1,
			envkey.ToolboxProjectEndpoint("tools-v1"): projectEndpoint,
			envkey.ToolboxMCPEndpoint("tools-v2"):     urlV2,
			envkey.ToolboxProjectEndpoint("tools-v2"): projectEndpoint,
		},
		projectServices: map[string]*azdext.ServiceConfig{
			"tools-v1": {Name: "tools-v1", Host: foundryToolboxHost},
			"tools-v2": {Name: "tools-v2", Host: foundryToolboxHost},
		},
		agentService: &azdext.ServiceConfig{
			Name: "agent",
			Host: foundryAgentHost,
			Uses: []string{"ai-project", "tools-v1", "tools-v2"},
		},
	}

	stored := map[string]*toolboxConnectionBody{}
	node := toolboxNode(g, managed.Toolboxes, func() (toolboxBuilder, error) {
		return &foundryToolboxBuilder{
			subscriptionID: "sub-1",
			resourceGroup:  "rg-1",
			accountName:    "acct",
			projectName:    "proj",
			explicitConns:  map[string]struct{}{},
			pipeline: runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					parts := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
					connName := parts[len(parts)-1]
					if req.Method == http.MethodGet {
						existing := stored[connName]
						if existing == nil {
							return &http.Response{
								StatusCode: http.StatusNotFound,
								Request:    req,
								Body:       io.NopCloser(strings.NewReader(`{}`)),
							}, nil
						}
						raw, _ := json.Marshal(existing)
						return &http.Response{
							StatusCode: http.StatusOK,
							Request:    req,
							Body:       io.NopCloser(bytes.NewReader(raw)),
						}, nil
					}
					var body toolboxConnectionBody
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						return nil, err
					}
					stored[connName] = &body
					raw, _ := json.Marshal(body)
					return &http.Response{
						StatusCode: http.StatusOK,
						Request:    req,
						Body:       io.NopCloser(bytes.NewReader(raw)),
					}, nil
				}),
			}),
		}, nil
	})
	require.NoError(t, node.Validate())
	require.NoError(t, node.Resolve(t.Context()))

	require.Len(t, managed.Tools, 2)
	require.Equal(t, map[string]any{
		"type":                  "mcp",
		"server_label":          "tools-v1",
		"server_url":            urlV1,
		"project_connection_id": "research-tools-mcp",
		"require_approval":      "never",
	}, managed.Tools[0])
	require.Equal(t, map[string]any{
		"type":                  "mcp",
		"server_label":          "tools-v2",
		"server_url":            urlV2,
		"project_connection_id": "research-tools-v2-mcp",
		"require_approval":      "always",
	}, managed.Tools[1])
}

func TestFoundryToolboxBuilder_RejectsIncompatibleOrExplicitConnection(t *testing.T) {
	t.Parallel()

	const projectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const mcpURL = projectEndpoint + "/toolboxes/research-tools/versions/1/mcp?api-version=v1"

	t.Run("incompatible existing connection is not overwritten", func(t *testing.T) {
		t.Parallel()
		putCalled := false
		pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPut {
					putCalled = true
				}
				existing := toolboxConnectionBody{
					Name: "research-tools-mcp",
					Properties: toolboxConnectionProperties{
						Category: "RemoteTool",
						AuthType: "ApiKey",
						Target:   "https://other.example.com/mcp",
					},
				}
				raw, _ := json.Marshal(existing)
				return &http.Response{
					StatusCode: http.StatusOK,
					Request:    req,
					Body:       io.NopCloser(bytes.NewReader(raw)),
				}, nil
			}),
		})
		builder := &foundryToolboxBuilder{
			subscriptionID: "sub-1",
			resourceGroup:  "rg-1",
			accountName:    "acct",
			projectName:    "proj",
			pipeline:       pipeline,
			explicitConns:  map[string]struct{}{},
		}
		_, err := builder.ResolveToolbox(t.Context(), toolboxRef{
			Name:        "research-tools",
			MCPEndpoint: mcpURL,
		})
		require.ErrorContains(t, err, `project connection "research-tools-mcp" already exists with incompatible`)
		require.False(t, putCalled)
	})

	t.Run("explicitly configured connection in azure.yaml is not overwritten", func(t *testing.T) {
		t.Parallel()
		putCalled := false
		pipeline := runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPut {
					putCalled = true
				}
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Request:    req,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			}),
		})
		builder := &foundryToolboxBuilder{
			subscriptionID: "sub-1",
			resourceGroup:  "rg-1",
			accountName:    "acct",
			projectName:    "proj",
			pipeline:       pipeline,
			explicitConns:  map[string]struct{}{"research-tools-mcp": {}},
		}
		_, err := builder.ResolveToolbox(t.Context(), toolboxRef{
			Name:        "research-tools",
			MCPEndpoint: mcpURL,
		})
		require.ErrorContains(t, err, `connection "research-tools-mcp" is explicitly declared in azure.yaml`)
		require.False(t, putCalled)
	})
}

func TestToolboxNode_MultipleToolboxesAndPreservesToolsAndApproval(t *testing.T) {
	t.Parallel()

	const projectEndpoint = "https://acct.services.ai.azure.com/api/projects/proj"
	const urlA = projectEndpoint + "/toolboxes/research-tools/versions/1/mcp?api-version=v1"
	const urlB = projectEndpoint + "/toolboxes/support-tools/versions/3/mcp?api-version=v1"

	managed := &agent_yaml.PromptAgent{
		Model:        "gpt-4.1-mini",
		Instructions: "Help.",
		Harness:      testMicrosoftCopilotHarness(),
		Tools: []any{
			map[string]any{"type": "web_search"},
			map[string]any{
				"type":             "mcp",
				"server_label":     "support-tools",
				"require_approval": "never",
			},
		},
		Toolboxes: []agent_yaml.ToolboxReference{
			{Name: "research-tools"},
			{Name: "support-tools"},
		},
	}
	g := &promptGraph{
		managed:  managed,
		bindings: map[string]any{},
		env: map[string]string{
			"FOUNDRY_PROJECT_ENDPOINT":                      projectEndpoint,
			envkey.ToolboxMCPEndpoint("research-tools"):     urlA,
			envkey.ToolboxProjectEndpoint("research-tools"): projectEndpoint,
			envkey.ToolboxMCPEndpoint("support-tools"):      urlB,
			envkey.ToolboxProjectEndpoint("support-tools"):  projectEndpoint,
		},
		projectServices: map[string]*azdext.ServiceConfig{
			"research-tools": {Name: "research-tools", Host: foundryToolboxHost},
			"support-tools":  {Name: "support-tools", Host: foundryToolboxHost},
		},
		agentService: &azdext.ServiceConfig{
			Name: "agent",
			Host: foundryAgentHost,
			Uses: []string{"ai-project", "research-tools", "support-tools"},
		},
	}

	node := toolboxNode(g, managed.Toolboxes, func() (toolboxBuilder, error) {
		return &foundryToolboxBuilder{
			subscriptionID: "sub-1",
			resourceGroup:  "rg-1",
			accountName:    "acct",
			projectName:    "proj",
			explicitConns:  map[string]struct{}{},
			pipeline: runtime.NewPipeline("test", "v1", runtime.PipelineOptions{}, &policy.ClientOptions{
				Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodGet {
						return &http.Response{
							StatusCode: http.StatusNotFound,
							Request:    req,
							Body:       io.NopCloser(strings.NewReader(`{}`)),
						}, nil
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Request:    req,
						Body:       io.NopCloser(strings.NewReader(`{}`)),
					}, nil
				}),
			}),
		}, nil
	})
	require.NoError(t, node.Validate())
	require.NoError(t, node.Resolve(t.Context()))

	require.Len(t, managed.Tools, 3)
	require.Equal(t, map[string]any{"type": "web_search"}, managed.Tools[0])
	require.Equal(t, map[string]any{
		"type":                  "mcp",
		"server_label":          "support-tools",
		"server_url":            urlB,
		"project_connection_id": "support-tools-mcp",
		"require_approval":      "never",
	}, managed.Tools[1])
	require.Equal(t, map[string]any{
		"type":                  "mcp",
		"server_label":          "research-tools",
		"server_url":            urlA,
		"project_connection_id": "research-tools-mcp",
		"require_approval":      "always",
	}, managed.Tools[2])
}
