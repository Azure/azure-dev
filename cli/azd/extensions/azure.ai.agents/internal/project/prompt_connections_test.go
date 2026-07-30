// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"
)

func TestResolveConnectionAction_Rung1_ExistingByName(t *testing.T) {
	existing := map[string]string{"aisearch-conn": "id-1"}
	decl := agent_yaml.PromptConnection{Name: "aisearch-conn", Category: "CognitiveSearch"}

	action, _, err := resolveConnectionAction(decl, existing, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != connActionUseExisting {
		t.Errorf("action: got %v, want use-existing", action)
	}
}

func TestResolveConnectionAction_Rung2_CreateWithTarget(t *testing.T) {
	decl := agent_yaml.PromptConnection{
		Name:     "aisearch-conn",
		Category: "CognitiveSearch",
		Target:   "https://s.search.windows.net",
		AuthType: "Entra",
	}
	action, resolved, err := resolveConnectionAction(decl, map[string]string{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != connActionCreate {
		t.Errorf("action: got %v, want create", action)
	}
	if resolved.Target != decl.Target {
		t.Errorf("target: got %q", resolved.Target)
	}
}

func TestResolveConnectionAction_Rung3_AutoFillTarget(t *testing.T) {
	decl := agent_yaml.PromptConnection{Name: "aisearch-conn", Category: "CognitiveSearch"}
	env := map[string]string{
		"AI_PROJECT_CONNECTIONS": "other=https://x; aisearch-conn=https://filled.search.windows.net",
	}
	action, resolved, err := resolveConnectionAction(decl, map[string]string{}, env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != connActionCreate {
		t.Errorf("action: got %v, want create", action)
	}
	if resolved.Target != "https://filled.search.windows.net" {
		t.Errorf("auto-filled target: got %q", resolved.Target)
	}
}

func TestResolveConnectionAction_Rung4_ProvisionOptIn(t *testing.T) {
	decl := agent_yaml.PromptConnection{Name: "search-conn", Category: "CognitiveSearch", Provision: true}
	action, _, err := resolveConnectionAction(decl, map[string]string{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if action != connActionProvision {
		t.Errorf("action: got %v, want provision", action)
	}
}

func TestResolveConnectionAction_Rung4_FailFastNoOptIn(t *testing.T) {
	decl := agent_yaml.PromptConnection{Name: "search-conn", Category: "CognitiveSearch"}
	action, _, err := resolveConnectionAction(decl, map[string]string{}, nil)
	if err == nil {
		t.Fatal("expected fail-fast error")
	}
	if action != connActionFailFast {
		t.Errorf("action: got %v, want fail-fast", action)
	}
}

func TestRequiredRoleForTool(t *testing.T) {
	roleID, roleName, ok := requiredRoleForTool("azure_ai_search")
	if !ok || roleID == "" || roleName != "Search Index Data Reader" {
		t.Errorf("azure_ai_search: got %q, %q, %v", roleID, roleName, ok)
	}
	if _, _, ok := requiredRoleForTool("code_interpreter"); ok {
		t.Error("code_interpreter should need no role")
	}
}

func TestTargetFromEnv(t *testing.T) {
	env := map[string]string{
		"AI_PROJECT_DEPENDENT_RESOURCES": "foo=https://foo; conn=https://target",
	}
	if got := targetFromEnv("conn", env); got != "https://target" {
		t.Errorf("target: got %q", got)
	}
	if got := targetFromEnv("missing", env); got != "" {
		t.Errorf("missing target: got %q", got)
	}
}

// fakeConnectionResolver records calls and simulates an existing set.
type fakeConnectionResolver struct {
	existing     map[string]string
	created      []agent_yaml.PromptConnection
	roleAssigned []string
}

func (r *fakeConnectionResolver) Existing(context.Context) (map[string]string, error) {
	if r.existing == nil {
		r.existing = map[string]string{}
	}
	return r.existing, nil
}

func (r *fakeConnectionResolver) Create(
	_ context.Context, decl agent_yaml.PromptConnection,
) (string, error) {
	r.created = append(r.created, decl)
	return "new-id", nil
}

func (r *fakeConnectionResolver) AssignRole(
	_ context.Context, _ agent_yaml.PromptConnection, _, roleName string,
) error {
	r.roleAssigned = append(r.roleAssigned, roleName)
	return nil
}

func TestConnectionsNode_CreatesMissingAndAssignsRole(t *testing.T) {
	managed := &agent_yaml.PromptAgent{
		Model:        "m",
		Instructions: "i",
		Connections: []agent_yaml.PromptConnection{
			{Name: "aisearch-conn", Category: "CognitiveSearch", Target: "https://s", AuthType: "Entra"},
		},
		Tools: []any{
			map[string]any{"type": "azure_ai_search", "connection": "aisearch-conn"},
		},
	}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, env: map[string]string{}, bindings: map[string]any{}}
	fake := &fakeConnectionResolver{}

	node := connectionsNode(g, func() (connectionResolver, error) { return fake, nil })
	if node == nil {
		t.Fatal("expected a connections node")
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if len(fake.created) != 1 || fake.created[0].Name != "aisearch-conn" {
		t.Errorf("created: got %+v", fake.created)
	}
	if len(fake.roleAssigned) != 1 || fake.roleAssigned[0] != "Search Index Data Reader" {
		t.Errorf("roles: got %+v", fake.roleAssigned)
	}
}

func TestConnectionsNode_UsesExistingNoCreate(t *testing.T) {
	managed := &agent_yaml.PromptAgent{
		Model:        "m",
		Instructions: "i",
		Connections: []agent_yaml.PromptConnection{
			{Name: "existing-conn", Category: "CognitiveSearch"},
		},
	}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, env: map[string]string{}, bindings: map[string]any{}}
	fake := &fakeConnectionResolver{existing: map[string]string{"existing-conn": "id-x"}}

	node := connectionsNode(g, func() (connectionResolver, error) { return fake, nil })
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(fake.created) != 0 {
		t.Errorf("expected no create for existing connection, got %+v", fake.created)
	}
}

func TestConnectionsNode_NoneReturnsNil(t *testing.T) {
	g := &promptGraph{managed: &agent_yaml.PromptAgent{}, bindings: map[string]any{}}
	node := connectionsNode(g, func() (connectionResolver, error) { return nil, nil })
	if node != nil {
		t.Fatal("expected nil node when no connections declared")
	}
}

func TestParseAccountProject(t *testing.T) {
	account, project, err := parseAccountProject(
		"https://myacct.services.ai.azure.com/api/projects/myproj",
	)
	if err != nil {
		t.Fatalf("parseAccountProject: %v", err)
	}
	if account != "myacct" || project != "myproj" {
		t.Errorf("got account=%q project=%q", account, project)
	}

	if _, _, err := parseAccountProject("not-a-url"); err == nil {
		t.Error("expected error for invalid endpoint")
	}
}
