// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"testing"

	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// fakeDeploymentResolver records existence checks and creations.
type fakeDeploymentResolver struct {
	exists  bool
	creates int
	checks  int
}

func (r *fakeDeploymentResolver) Exists(context.Context, string) (bool, error) {
	r.checks++
	return r.exists, nil
}

func (r *fakeDeploymentResolver) Create(context.Context, string) error {
	r.creates++
	return nil
}

func TestDeploymentNode_CreatesWhenMissing(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeDeploymentResolver{exists: false}

	node := deploymentNode(g, func() (deploymentResolver, error) { return fake, nil })
	if node == nil {
		t.Fatal("expected a deployment node")
	}
	if err := node.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if fake.checks != 1 || fake.creates != 1 {
		t.Errorf("expected 1 check + 1 create, got %d/%d", fake.checks, fake.creates)
	}
}

func TestDeploymentNode_SkipsCreateWhenExists(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	fake := &fakeDeploymentResolver{exists: true}

	node := deploymentNode(g, func() (deploymentResolver, error) { return fake, nil })
	if err := node.Resolve(context.Background()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if fake.creates != 0 {
		t.Errorf("expected no create when deployment exists, got %d", fake.creates)
	}
}

func TestDeploymentNode_ValidateRejectsBadModelName(t *testing.T) {
	managed := &agent_yaml.PromptAgent{Model: "not a/valid name", Instructions: "i"}
	managed.Name = "agent"
	g := &promptGraph{managed: managed, bindings: map[string]any{}}
	node := deploymentNode(g, func() (deploymentResolver, error) {
		return &fakeDeploymentResolver{}, nil
	})
	if err := node.Validate(); err == nil {
		t.Error("expected validation error for invalid model name")
	}
}

// TestGraphResolve_ValidatesAllBeforeAnyMutation proves the graph runs every
// node's Validate before any Resolve, so a validation failure never leaves a
// half-wired agent (no Resolve side effects occur).
func TestGraphResolve_ValidatesAllBeforeAnyMutation(t *testing.T) {
	resolved := 0
	g := &promptGraph{
		managed:  &agent_yaml.PromptAgent{},
		bindings: map[string]any{},
		nodes: []promptNode{
			{
				Kind:     nodeFileStore,
				Validate: func() error { return nil },
				Resolve: func(context.Context) error {
					resolved++
					return nil
				},
			},
			{
				Kind:     nodeConnection,
				Validate: func() error { return errors.New("bad connection") },
				Resolve: func(context.Context) error {
					resolved++
					return nil
				},
			},
		},
	}

	err := g.resolve(context.Background(), azdext.ProgressReporter(nil))
	if err == nil {
		t.Fatal("expected validation error")
	}
	if resolved != 0 {
		t.Errorf("no Resolve should run when validation fails; ran %d", resolved)
	}
}

// TestGraphResolve_ResolvesInOrderWhenValid confirms all nodes resolve when
// validation passes.
func TestGraphResolve_ResolvesInOrderWhenValid(t *testing.T) {
	var order []promptNodeKind
	g := &promptGraph{
		managed:  &agent_yaml.PromptAgent{},
		bindings: map[string]any{},
		nodes: []promptNode{
			{
				Kind:     nodeDeployment,
				Validate: func() error { return nil },
				Resolve: func(context.Context) error {
					order = append(order, nodeDeployment)
					return nil
				},
			},
			{
				Kind:     nodeAgent,
				Validate: func() error { return nil },
				Resolve: func(context.Context) error {
					order = append(order, nodeAgent)
					return nil
				},
			},
		},
	}

	if err := g.resolve(context.Background(), azdext.ProgressReporter(nil)); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(order) != 2 || order[0] != nodeDeployment || order[1] != nodeAgent {
		t.Errorf("resolve order: got %v", order)
	}
}
