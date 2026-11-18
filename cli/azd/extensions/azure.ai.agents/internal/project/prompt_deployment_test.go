// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
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
				Kind:     nodeMemoryStore,
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

func TestProvisionedDeploymentResolver_PendingProvisionSignal(t *testing.T) {
	t.Parallel()

	for _, pending := range []string{
		"model_deployment",
		"project",
		"acr, model_deployment",
		"project,app_insights",
	} {
		t.Run(pending, func(t *testing.T) {
			t.Parallel()

			managed := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "i"}
			managed.Name = "agent"
			env := map[string]string{
				"AI_AGENT_PENDING_PROVISION": pending,
			}
			g, err := newPromptGraph(t.TempDir(), managed, &PromptAgentSettings{}, env, nil)
			require.NoError(t, err)

			err = g.resolve(t.Context(), nil)
			require.Error(t, err)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Same(t, localErr, err, "deploymentNode must return structured LocalError without fmt.Errorf wrapping")
			require.Equal(t, exterrors.CodeModelDeploymentNotFound, localErr.Code)
			require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
			require.Contains(t, localErr.Message, `"gpt-4.1-mini"`)
			require.Contains(t, localErr.Suggestion, "azd provision")
		})
	}
}

func TestProvisionedDeploymentResolver_DeclaredProjectDeploymentState(t *testing.T) {
	t.Parallel()

	newProjectServices := func(t *testing.T, modelName string) (*azdext.ServiceConfig, map[string]*azdext.ServiceConfig) {
		t.Helper()
		props, err := structpb.NewStruct(map[string]any{
			"deployments": []any{
				map[string]any{"name": modelName},
			},
		})
		require.NoError(t, err)
		agentSvc := &azdext.ServiceConfig{
			Name: "agent",
			Host: "azure.ai.agent",
			Uses: []string{"ai-project"},
		}
		projectServices := map[string]*azdext.ServiceConfig{
			"ai-project": {
				Name:                 "ai-project",
				Host:                 FoundryProjectHost,
				AdditionalProperties: props,
			},
			"agent": agentSvc,
		}
		return agentSvc, projectServices
	}

	t.Run("declared deployment without AI_PROJECT_DEPLOYMENTS succeeds when pending is empty", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0o600))
		agentSvc, projectServices := newProjectServices(t, "gpt-4.1-mini")
		managed := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "i"}
		managed.Name = "agent"

		g, err := newPromptGraph(root, managed, &PromptAgentSettings{}, map[string]string{}, nil)
		require.NoError(t, err)
		g.projectRoot = root
		g.agentService = agentSvc
		g.projectServices = projectServices

		require.NoError(t, g.resolve(t.Context(), nil))
	})

	t.Run("declared deployment fails when AI_AGENT_PENDING_PROVISION has model_deployment", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0o600))
		agentSvc, projectServices := newProjectServices(t, "gpt-4.1-mini")
		managed := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "i"}
		managed.Name = "agent"
		env := map[string]string{
			"AI_AGENT_PENDING_PROVISION": "model_deployment",
		}

		g, err := newPromptGraph(root, managed, &PromptAgentSettings{}, env, nil)
		require.NoError(t, err)
		g.projectRoot = root
		g.agentService = agentSvc
		g.projectServices = projectServices

		err = g.resolve(t.Context(), nil)
		require.Error(t, err)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Same(t, localErr, err)
		require.Equal(t, exterrors.CodeModelDeploymentNotFound, localErr.Code)
		require.Equal(t, azdext.LocalErrorCategoryDependency, localErr.Category)
	})

	t.Run("declared deployment succeeds when AI_PROJECT_DEPLOYMENTS is set and pending is empty", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0o600))
		agentSvc, projectServices := newProjectServices(t, "gpt-4.1-mini")
		managed := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "i"}
		managed.Name = "agent"
		env := map[string]string{
			"AI_PROJECT_DEPLOYMENTS": `[{"name":"gpt-4.1-mini"}]`,
		}

		g, err := newPromptGraph(root, managed, &PromptAgentSettings{}, env, nil)
		require.NoError(t, err)
		g.projectRoot = root
		g.agentService = agentSvc
		g.projectServices = projectServices

		require.NoError(t, g.resolve(t.Context(), nil))
	})

	t.Run("undeclared model deployment reuse succeeds without AI_PROJECT_DEPLOYMENTS", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0o600))
		agentSvc, projectServices := newProjectServices(t, "other-deployment")
		managed := &agent_yaml.PromptAgent{Model: "reused-existing-deployment", Instructions: "i"}
		managed.Name = "agent"

		g, err := newPromptGraph(root, managed, &PromptAgentSettings{}, map[string]string{}, nil)
		require.NoError(t, err)
		g.projectRoot = root
		g.agentService = agentSvc
		g.projectServices = projectServices

		require.NoError(t, g.resolve(t.Context(), nil))
	})

	t.Run("declared deployment succeeds when infrastructure is on disk", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), []byte("name: test\n"), 0o600))
		infraDir := filepath.Join(root, "infra")
		require.NoError(t, os.MkdirAll(infraDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(infraDir, "main.bicep"), []byte("// on disk\n"), 0o600))
		agentSvc, projectServices := newProjectServices(t, "gpt-4.1-mini")
		managed := &agent_yaml.PromptAgent{Model: "gpt-4.1-mini", Instructions: "i"}
		managed.Name = "agent"

		g, err := newPromptGraph(root, managed, &PromptAgentSettings{}, map[string]string{}, nil)
		require.NoError(t, err)
		g.projectRoot = root
		g.agentService = agentSvc
		g.projectServices = projectServices

		require.NoError(t, g.resolve(t.Context(), nil))
	})
}
