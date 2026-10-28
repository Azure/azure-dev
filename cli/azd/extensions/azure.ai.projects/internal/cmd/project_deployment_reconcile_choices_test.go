// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"testing"

	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sequencedAdoptionPrompt answers Select prompts in order and records them.
type sequencedAdoptionPrompt struct {
	azdext.UnimplementedPromptServiceServer
	selections []int32
	requests   []*azdext.SelectRequest
	modelCalls int
	modelQuota *azdext.QuotaCheckOptions
}

func (s *sequencedAdoptionPrompt) Select(
	_ context.Context, request *azdext.SelectRequest,
) (*azdext.SelectResponse, error) {
	s.requests = append(s.requests, request)
	if len(s.selections) == 0 {
		return nil, status.Error(codes.Internal, "unexpected selection")
	}
	index := s.selections[0]
	s.selections = s.selections[1:]
	return &azdext.SelectResponse{Value: new(index)}, nil
}

func (s *sequencedAdoptionPrompt) PromptAiModel(
	_ context.Context, request *azdext.PromptAiModelRequest,
) (*azdext.PromptAiModelResponse, error) {
	s.modelCalls++
	s.modelQuota = request.GetQuota()
	return &azdext.PromptAiModelResponse{Model: &azdext.AiModel{Name: "gpt-4.1"}}, nil
}

func choiceLabels(request *azdext.SelectRequest) []string {
	labels := []string{}
	for _, choice := range request.GetOptions().GetChoices() {
		labels = append(labels, choice.GetLabel())
	}
	return labels
}

func TestAdoptedDeploymentChoicesDifferentModel(t *testing.T) {
	declared := synthesis.Deployment{
		Name:  "template-chat",
		Model: synthesis.DeploymentModel{Name: "gpt-4o", Format: "OpenAI", Version: "2024-08-06"},
		Sku:   synthesis.DeploymentSku{Name: "GlobalStandard", Capacity: 10},
	}
	matching := liveProjectDeployment{Name: "existing-4o", Model: declared.Model}
	other := liveProjectDeployment{
		Name:  "other-41",
		Model: synthesis.DeploymentModel{Name: "gpt-4.1", Format: "OpenAI", Version: "2025-04-14"},
		Sku:   synthesis.DeploymentSku{Name: "Standard", Capacity: 5},
	}
	target := &resolvedProject{
		SubscriptionId: "subscription", UserTenantId: "tenant", Location: "eastus",
	}
	for _, test := range []struct {
		name            string
		live            []liveProjectDeployment
		selections      []int32
		noPrompt        bool
		wantLabels      []string
		wantList        []string
		wantDefault     string
		wantChanged     bool
		wantCatalog     bool
		originalDefault string
	}{
		{
			name:            "explicit reuse replaces a live default",
			live:            []liveProjectDeployment{matching, other},
			selections:      []int32{2, 1},
			originalDefault: matching.Name,
			wantList:        []string{},
			wantDefault:     other.Name,
			wantChanged:     true,
		},
		{
			name:            "explicit catalog choice replaces a live default",
			live:            []liveProjectDeployment{other},
			selections:      []int32{1, 1},
			originalDefault: other.Name,
			wantList:        []string{"template-chat:gpt-4.1"},
			wantDefault:     "template-chat",
			wantChanged:     true,
			wantCatalog:     true,
		},
		{
			name:            "ordinary reuse preserves a live default",
			live:            []liveProjectDeployment{matching, other},
			selections:      []int32{0},
			originalDefault: other.Name,
			wantList:        []string{},
			wantDefault:     other.Name,
			wantChanged:     true,
		},
		{
			name:        "no match now prompts and keeps the template by default",
			live:        []liveProjectDeployment{other},
			selections:  []int32{0},
			wantLabels:  []string{"Deploy as configured", "Choose a different model", "Skip this model"},
			wantList:    []string{"template-chat:gpt-4o"},
			wantDefault: "template-chat",
			wantChanged: true,
		},
		{
			name:        "choose an existing deployment of another model",
			live:        []liveProjectDeployment{matching, other},
			selections:  []int32{2, 1},
			wantList:    []string{},
			wantDefault: "other-41",
			wantChanged: true,
		},
		{
			name:        "choose a catalog model under the declared name",
			live:        []liveProjectDeployment{other},
			selections:  []int32{1, 1},
			wantList:    []string{"template-chat:gpt-4.1"},
			wantDefault: "template-chat",
			wantChanged: true,
			wantCatalog: true,
		},
		{
			name:       "default still reuses the first matching deployment",
			live:       []liveProjectDeployment{matching, other},
			selections: []int32{0},
			wantLabels: []string{
				`Use existing deployment "existing-4o" (version: 2024-08-06, SKU: )`,
				"Deploy as configured", "Choose a different model", "Skip this model",
			},
			wantList:    []string{},
			wantDefault: "existing-4o",
			wantChanged: true,
		},
		{
			name:        "no prompt keeps an unmatched declaration",
			live:        []liveProjectDeployment{other},
			noPrompt:    true,
			wantDefault: "template-chat",
		},
		{
			name:        "no prompt reuses the first matching deployment",
			live:        []liveProjectDeployment{matching, other},
			noPrompt:    true,
			wantList:    []string{},
			wantDefault: "existing-4o",
			wantChanged: true,
		},
		{
			name:        "skip replaces the existing binding",
			live:        []liveProjectDeployment{matching},
			selections:  []int32{3},
			wantList:    []string{},
			wantChanged: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			prompt := &sequencedAdoptionPrompt{selections: test.selections}
			client, project, env, ai, _ := newSelfInitializingDeploymentClient(t, t.TempDir(), prompt)
			project.services["project"] = map[string]any{"deployments": []any{deploymentMap(declared)}}
			const originalBindings = `{"Template-Chat":"old-selection","other":"untouched"}`
			values := map[string]string{
				"AZURE_AI_MODEL_DEPLOYMENT_NAME":   test.originalDefault,
				synthesis.DeploymentBindingsEnvKey: originalBindings,
			}
			env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"] = test.originalDefault
			env.values[synthesis.DeploymentBindingsEnvKey] = originalBindings
			defaultName, changed, restore, err := reconcileAdoptedDeploymentChoices(
				t.Context(), client, "test", target,
				[]synthesis.Deployment{declared}, test.live, values, test.noPrompt,
			)
			require.NoError(t, err)
			assert.Equal(t, test.wantChanged, changed)
			assert.Equal(t, test.wantDefault, defaultName)
			assert.Equal(t, test.wantDefault, env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
			assert.Empty(t, prompt.selections, "every scripted answer is consumed")
			if test.noPrompt {
				assert.Empty(t, prompt.requests)
			}
			if test.wantLabels != nil {
				require.NotEmpty(t, prompt.requests)
				assert.Equal(t, test.wantLabels, choiceLabels(prompt.requests[0]))
				assert.Zero(t, prompt.requests[0].GetOptions().GetSelectedIndex())
			}
			assert.Equal(t, test.wantCatalog, prompt.modelCalls == 1)
			if test.wantCatalog {
				require.NotNil(t, prompt.modelQuota)
				assert.Equal(t, float64(1), prompt.modelQuota.GetMinRemainingCapacity())
			}
			assert.Equal(t, test.wantCatalog, ai.calls > 0)
			assert.False(t, project.deploymentSet, "adoption must never write portable deployments")
			bindings, err := synthesis.DecodeDeploymentBindings(env.values[synthesis.DeploymentBindingsEnvKey])
			require.NoError(t, err)
			wantBinding := test.wantDefault
			if test.name == "ordinary reuse preserves a live default" {
				wantBinding = matching.Name
			}
			require.Equal(t, map[string]string{
				"Template-Chat": wantBinding, "other": "untouched",
			}, bindings)
			got, present, err := provisioning.DecodeProjectDeployments(env.values[projectDeploymentsEnvKey])
			require.NoError(t, err)
			if test.wantList == nil {
				assert.False(t, present)
			} else {
				require.True(t, present)
				names := []string{}
				for _, deployment := range got {
					names = append(names, deployment.Name+":"+deployment.Model.Name)
				}
				assert.Equal(t, test.wantList, names)
			}
			require.NoError(t, restore())
			assert.Equal(t, test.originalDefault, env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
			assert.Equal(t, originalBindings, env.values[synthesis.DeploymentBindingsEnvKey])
		})
	}

}

func TestAdoptedDeploymentChoicesPreservesEachBinding(t *testing.T) {
	declared := []synthesis.Deployment{
		{Name: "chat", Model: synthesis.DeploymentModel{Name: "gpt-4o"}},
		{Name: "embeddings", Model: synthesis.DeploymentModel{Name: "text-embedding-3-small"}},
		{Name: "skipped", Model: synthesis.DeploymentModel{Name: "other-model"}},
	}
	live := []liveProjectDeployment{
		{Name: "existing-chat", Model: declared[0].Model},
		{Name: "existing-embeddings", Model: declared[1].Model},
	}
	prompt := &sequencedAdoptionPrompt{selections: []int32{0, 0, 2}}
	client, _, env, _, _ := newSelfInitializingDeploymentClient(t, t.TempDir(), prompt)
	_, _, restore, err := reconcileAdoptedDeploymentChoices(
		t.Context(), client, "test", nil, declared, live, nil, false,
	)
	require.NoError(t, err)
	bindings, err := synthesis.DecodeDeploymentBindings(env.values[synthesis.DeploymentBindingsEnvKey])
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"chat": "existing-chat", "embeddings": "existing-embeddings", "skipped": "",
	}, bindings)
	require.Empty(t, prompt.selections)
	require.NoError(t, restore())
	require.Empty(t, env.values[synthesis.DeploymentBindingsEnvKey])
}
