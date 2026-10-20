// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"maps"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"
)

type adoptedReplacementEnvironment struct {
	promptEnvironmentServer
	failKey string
	writes  int
}

func (s *adoptedReplacementEnvironment) SetValue(
	_ context.Context, request *azdext.SetEnvRequest,
) (*azdext.EmptyResponse, error) {
	s.writes++
	s.values[request.Key] = request.Value
	if request.Key == s.failKey {
		s.failKey = ""
		return nil, errors.New("environment write failed")
	}
	return &azdext.EmptyResponse{}, nil
}

func TestReplaceAdoptedTemplateDeploymentPreservesStateOnFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		failKey  string
		multiple bool
		wantErr  string
	}{
		{"conflict before writes", "", true, "cannot tell which"},
		{"default write failure", "AZURE_AI_MODEL_DEPLOYMENT_NAME", false, "environment write failed"},
		{"list write failure", projectDeploymentsEnvKey, false, "environment write failed"},
		{"success", "", false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			declared := []project.Deployment{adoptedTestDeployment("template-chat", "gpt-4o")}
			if test.multiple {
				declared = append(declared, adoptedTestDeployment("embedding", "text-embedding-3-small"))
			}
			list, err := project.EncodeProjectDeploymentsEnv(declared)
			require.NoError(t, err)
			raw := []any{}
			for _, deployment := range declared {
				raw = append(raw, map[string]any{"name": deployment.Name})
			}
			server := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: t.TempDir(),
				Services: map[string]*azdext.ServiceConfig{
					"project": {
						Name: "project", Host: project.FoundryProjectHost,
						AdditionalProperties: mustStruct(t, map[string]any{"deployments": raw}),
					},
				},
			}}
			env := &adoptedReplacementEnvironment{
				promptEnvironmentServer: promptEnvironmentServer{values: map[string]string{
					"AZURE_AI_MODEL_DEPLOYMENT_NAME": "old-default",
					projectDeploymentsEnvKey:         list,
				}},
				failKey: test.failKey,
			}
			before := maps.Clone(env.values)
			client := newHelpersTestAzdClient(t, server, &helpersPromptServer{}, env)
			err = replaceAdoptedTemplateDeployment(t.Context(), client, "test", "existing", true)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				require.Equal(t, before, env.values)
				if test.multiple {
					require.Zero(t, env.writes)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, "existing", env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
			require.Equal(t, "[]", env.values[projectDeploymentsEnvKey])
		})
	}
}

func adoptedTestDeployment(name, model string) project.Deployment {
	return project.Deployment{
		Name:  name,
		Model: project.DeploymentModel{Name: model, Format: "OpenAI", Version: "1"},
		Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 1},
	}
}

func decodedNames(t *testing.T, value string) []string {
	t.Helper()
	deployments, present, err := project.DecodeProjectDeploymentsEnv(value)
	require.NoError(t, err)
	require.True(t, present)
	names := []string{}
	for _, deployment := range deployments {
		names = append(names, deployment.Name)
	}
	return names
}

func noChoice(t *testing.T) adoptedDeploymentChooser {
	return func(string, []string) (string, error) {
		t.Fatal("unexpected prompt")
		return "", nil
	}
}

func TestPlanAdoptedDeploymentReplacement(t *testing.T) {
	chat := adoptedTestDeployment("template-chat", "gpt-4.1-nano")
	embed := adoptedTestDeployment("template-embed", "text-embedding-3-small")

	t.Run("single declared deployment writes empty list", func(t *testing.T) {
		value, write, err := planAdoptedDeploymentReplacement(
			[]project.Deployment{chat}, "", "other-4o", false, noChoice(t),
		)
		require.NoError(t, err)
		require.True(t, write)
		require.Empty(t, decodedNames(t, value))
	})

	t.Run("several declared deployments prompt and keep the rest", func(t *testing.T) {
		var offered []string
		value, write, err := planAdoptedDeploymentReplacement(
			[]project.Deployment{chat, embed}, "", "other-4o", false,
			func(existing string, names []string) (string, error) {
				require.Equal(t, "other-4o", existing)
				offered = names
				return "template-chat", nil
			},
		)
		require.NoError(t, err)
		require.True(t, write)
		require.Equal(t, []string{"template-chat", "template-embed"}, offered)
		require.Equal(t, []string{"template-embed"}, decodedNames(t, value))
		deployments, _, err := project.DecodeProjectDeploymentsEnv(value)
		require.NoError(t, err)
		require.Equal(t, embed, deployments[0])
	})

	t.Run("several declared deployments with no prompt fail", func(t *testing.T) {
		_, write, err := planAdoptedDeploymentReplacement(
			[]project.Deployment{chat, embed}, "", "other-4o", true, noChoice(t),
		)
		require.False(t, write)
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Equal(t, exterrors.CodeConflictingArguments, localErr.Code)
		require.Contains(t, err.Error(), "template-chat, template-embed")
		require.Equal(t, "run `azd ai agent init` without --no-prompt to choose", localErr.Suggestion)
	})

	t.Run("existing environment list keeps other entries", func(t *testing.T) {
		existingList, err := project.EncodeProjectDeploymentsEnv([]project.Deployment{
			chat, adoptedTestDeployment("custom", "gpt-4o"),
		})
		require.NoError(t, err)
		value, write, err := planAdoptedDeploymentReplacement(
			[]project.Deployment{chat, embed}, existingList, "other-4o", false,
			func(string, []string) (string, error) { return "template-chat", nil },
		)
		require.NoError(t, err)
		require.True(t, write)
		require.Equal(t, []string{"custom"}, decodedNames(t, value))
	})

	t.Run("no declared deployments leave the environment unchanged", func(t *testing.T) {
		value, write, err := planAdoptedDeploymentReplacement(nil, "", "other-4o", true, noChoice(t))
		require.NoError(t, err)
		require.False(t, write)
		require.Empty(t, value)
	})
}

func TestAdoptedTemplateDeploymentsExpandsEnvironment(t *testing.T) {
	props, err := structpb.NewStruct(map[string]any{"deployments": []any{
		map[string]any{
			"name":  "template-chat",
			"model": map[string]any{"name": "${CHAT_MODEL}", "format": "OpenAI", "version": "1"},
			"sku":   map[string]any{"name": "GlobalStandard", "capacity": "${CHAT_CAPACITY}"},
		},
	}})
	require.NoError(t, err)
	services := map[string]*azdext.ServiceConfig{
		"ai-project": {Name: "ai-project", Host: project.FoundryProjectHost, AdditionalProperties: props},
	}
	deployments, err := adoptedTemplateDeployments(services, t.TempDir(), map[string]string{
		"CHAT_MODEL": "gpt-4.1-nano", "CHAT_CAPACITY": "5",
	})
	require.NoError(t, err)
	require.Equal(t, []project.Deployment{{
		Name:  "template-chat",
		Model: project.DeploymentModel{Name: "gpt-4.1-nano", Format: "OpenAI", Version: "1"},
		Sku:   project.DeploymentSku{Name: "GlobalStandard", Capacity: 5},
	}}, deployments)
}
