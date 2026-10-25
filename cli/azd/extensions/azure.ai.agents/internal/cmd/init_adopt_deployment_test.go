// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"
	"azureaiagent/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
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
		name    string
		failKey string
		wantErr string
	}{
		{"default write failure", "AZURE_AI_MODEL_DEPLOYMENT_NAME", "environment write failed"},
		{"list write failure", projectDeploymentsEnvKey, "environment write failed"},
		{"bindings write failure", synthesis.DeploymentBindingsEnvKey, "environment write failed"},
		{"success", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			declared := []project.Deployment{adoptedTestDeployment("template-chat", "gpt-4o")}
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
					"AZURE_AI_MODEL_DEPLOYMENT_NAME":   "old-default",
					projectDeploymentsEnvKey:           list,
					synthesis.DeploymentBindingsEnvKey: `{"prior":"prior-selection","Template-Chat":"old-selection"}`,
				}},
				failKey: test.failKey,
			}
			before := maps.Clone(env.values)
			client := newHelpersTestAzdClient(t, server, &helpersPromptServer{}, env)
			err = replaceAdoptedTemplateDeployment(t.Context(), client, "test", "existing", "template-chat")
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				require.Equal(t, before, env.values)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "existing", env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
			require.Equal(t, "[]", env.values[projectDeploymentsEnvKey])
			bindings, err := synthesis.DecodeDeploymentBindings(env.values[synthesis.DeploymentBindingsEnvKey])
			require.NoError(t, err)
			require.Equal(t, map[string]string{
				"prior": "prior-selection", "Template-Chat": "existing",
			}, bindings)
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

func writeAdoptedDeploymentTemplate(
	t *testing.T, root string, referenced bool, names ...string,
) []byte {
	t.Helper()
	deployments := make([]any, 0, len(names))
	for _, name := range names {
		deployments = append(deployments, map[string]any{"name": name})
	}
	properties := map[string]any{"deployments": deployments}
	if referenced {
		content, err := yaml.Marshal(properties)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(root, "project.yaml"), content, 0600))
		properties = map[string]any{"$ref": "./project.yaml"}
	}
	properties["host"] = project.FoundryProjectHost
	content, err := yaml.Marshal(map[string]any{
		"name": "adoption",
		"services": map[string]any{
			"project": properties,
			"agent": map[string]any{
				"host": AiAgentHost, "kind": "prompt", "name": "sample-agent",
				"model": "template-chat", "uses": []string{"project"},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), content, 0600))
	return content
}

func TestPlanAdoptedDeploymentReplacement(t *testing.T) {
	chat := adoptedTestDeployment("template-chat", "gpt-4.1-nano")
	embed := adoptedTestDeployment("template-embed", "text-embedding-3-small")

	t.Run("single declared deployment writes empty list", func(t *testing.T) {
		value, write, err := planAdoptedDeploymentReplacement(
			[]project.Deployment{chat}, "", "template-chat",
		)
		require.NoError(t, err)
		require.True(t, write)
		require.Empty(t, decodedNames(t, value))
	})

	t.Run("selected declaration must still exist", func(t *testing.T) {
		_, _, err := planAdoptedDeploymentReplacement([]project.Deployment{chat}, "", "removed-template")
		localErr, ok := errors.AsType[*azdext.LocalError](err)
		require.True(t, ok)
		require.Equal(t, exterrors.CodeInvalidServiceConfig, localErr.Code)
		require.ErrorContains(t, err, "no longer declared")
	})

	t.Run("selected declaration matches case-insensitively", func(t *testing.T) {
		value, write, err := planAdoptedDeploymentReplacement([]project.Deployment{chat}, "", "TEMPLATE-CHAT")
		require.NoError(t, err)
		require.True(t, write)
		require.Empty(t, decodedNames(t, value))
	})

	t.Run("several declared deployments prompt during selection and keep the rest", func(t *testing.T) {
		var offered []string
		replaced, err := selectAdoptedDeploymentReplacement(
			[]project.Deployment{chat, embed}, "other-4o", false,
			func(existing string, names []string) (string, error) {
				require.Equal(t, "other-4o", existing)
				offered = names
				return "template-chat", nil
			},
		)
		require.NoError(t, err)
		value, write, err := planAdoptedDeploymentReplacement(
			[]project.Deployment{chat, embed}, "", replaced,
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
		_, err := selectAdoptedDeploymentReplacement(
			[]project.Deployment{chat, embed}, "other-4o", true, noChoice(t),
		)
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
			[]project.Deployment{chat, embed}, existingList, "template-chat",
		)
		require.NoError(t, err)
		require.True(t, write)
		require.Equal(t, []string{"custom"}, decodedNames(t, value))
	})

	t.Run("no declared deployments leave the environment unchanged", func(t *testing.T) {
		value, write, err := planAdoptedDeploymentReplacement(nil, "", "")
		require.NoError(t, err)
		require.False(t, write)
		require.Empty(t, value)
	})
}

func TestAdoptedTemplateDeploymentsExpandsEnvironment(t *testing.T) {
	t.Setenv("CHAT_MODEL", "process-model")
	t.Setenv("CHAT_CAPACITY", "9")
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
	deployments, err = adoptedTemplateDeployments(services, t.TempDir(), nil)
	require.NoError(t, err)
	require.Equal(t, "process-model", deployments[0].Model.Name)
	require.Equal(t, 9, deployments[0].Sku.Capacity)
	value, write, err := planAdoptedDeploymentReplacement(
		append([]project.Deployment{adoptedTestDeployment("replaced", "gpt-4o")}, deployments...),
		"", "replaced",
	)
	require.NoError(t, err)
	require.True(t, write)
	remaining, _, err := project.DecodeProjectDeploymentsEnv(value)
	require.NoError(t, err)
	require.Equal(t, deployments, remaining)
}

func TestValidateInitModelSelectors(t *testing.T) {
	for _, test := range []struct {
		name     string
		flags    initFlags
		adopting bool
		code     string
		flag     string
	}{
		{"neither", initFlags{}, true, "", ""},
		{"deployment alone", initFlags{modelDeployment: "existing", modelDeploymentExplicit: true}, true, "", ""},
		{"model alone", initFlags{model: "gpt-4o", modelExplicit: true}, true, "", ""},
		{"empty deployment", initFlags{modelDeploymentExplicit: true}, true,
			exterrors.CodeInvalidParameter, "--model-deployment"},
		{"whitespace deployment", initFlags{modelDeployment: " \t", modelDeploymentExplicit: true}, true,
			exterrors.CodeInvalidParameter, "--model-deployment"},
		{"empty model", initFlags{modelExplicit: true}, true, exterrors.CodeInvalidParameter, "--model"},
		{"whitespace model", initFlags{model: " \t", modelExplicit: true}, true,
			exterrors.CodeInvalidParameter, "--model"},
		{"both", initFlags{modelDeployment: "existing", model: "gpt-4o",
			modelDeploymentExplicit: true, modelExplicit: true}, true,
			exterrors.CodeConflictingArguments, "--model-deployment and --model"},
		{"non-adopt precedence preserved", initFlags{modelDeployment: "existing", model: "gpt-4o",
			modelDeploymentExplicit: true, modelExplicit: true}, false, "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateInitModelSelectors(&test.flags, test.adopting)
			if test.code == "" {
				require.NoError(t, err)
				return
			}
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, test.code, localErr.Code)
			require.Contains(t, localErr.Message, test.flag)
			require.Contains(t, localErr.Suggestion, "--model-deployment")
			require.Contains(t, localErr.Suggestion, "--model")
			// No client is available, so reaching any project or Azure operation would fail.
			err = finalizeAdoptedProject(t.Context(), &test.flags, nil, "test", "", false,
				"renamed-agent", TemplateTypeAzureYaml, "")
			require.EqualError(t, err, localErr.Error())
		})
	}
}

func TestInitModelSelectorsValidateExplicitFlagsBeforeClientCreation(t *testing.T) {
	for _, args := range [][]string{
		{"--model-deployment="},
		{"-d", " \t"},
		{"--model="},
		{"--model", " \t"},
		{"--template", "azure.yaml", "--model-deployment", "existing", "--model", "gpt-4o"},
	} {
		t.Run(args[0]+args[len(args)-1], func(t *testing.T) {
			cmd := newInitCommand(&azdext.ExtensionContext{})
			cmd.SetContext(t.Context())
			require.NoError(t, cmd.ParseFlags(args))
			err := cmd.RunE(cmd, nil)
			localErr, ok := errors.AsType[*azdext.LocalError](err)
			require.True(t, ok)
			require.Equal(t, azdext.LocalErrorCategoryValidation, localErr.Category)
			require.Contains(t, localErr.Message, "--model")
		})
	}
}

func TestAdoptedDeploymentPreflightBeforeScaffolding(t *testing.T) {
	for _, templateType := range []string{TemplateTypeAzureYaml, TemplateTypeAzd} {
		for _, layout := range []struct {
			name           string
			referenced     bool
			existingTarget bool
		}{
			{"inline-new-target", false, false},
			{"referenced-existing-target", true, true},
		} {
			for _, test := range []struct {
				name      string
				noPrompt  bool
				promptErr error
				code      string
			}{
				{"ambiguous headless selection", true, nil, exterrors.CodeConflictingArguments},
				{"cancelled interactive selection", false,
					status.Error(codes.Canceled, "cancelled"), exterrors.CodeCancelled},
			} {
				t.Run(templateType+"/"+layout.name+"/"+test.name, func(t *testing.T) {
					root := t.TempDir()
					t.Chdir(root)
					target := filepath.Join(root, "target")
					envPath := filepath.Join(target, ".azure", "test", ".env")
					originalEnv := []byte("UNCHANGED=yes\n")
					if layout.existingTarget {
						require.NoError(t, os.MkdirAll(filepath.Dir(envPath), 0700))
						require.NoError(t, os.WriteFile(envPath, originalEnv, 0600))
					}
					source := t.TempDir()
					content := writeAdoptedDeploymentTemplate(t, source, layout.referenced, "chat", "embed")
					env := &testEnvironmentServiceServer{values: map[string]map[string]string{
						"test": {"UNCHANGED": "yes"},
					}}
					before := maps.Clone(env.values["test"])
					workflow := &testWorkflowServiceServer{runHook: func() {
						require.NoError(t, os.MkdirAll(filepath.Dir(envPath), 0700))
						require.NoError(t, os.WriteFile(filepath.Join(target, "azure.yaml"), content, 0600))
						require.NoError(t, os.WriteFile(envPath, []byte("CREATED=yes\n"), 0600))
					}}
					prompt := &helpersPromptServer{selectErr: test.promptErr}
					client := newTestAzdClient(t, env, workflow, prompt)
					flags := &initFlags{
						src: target, env: "test", templatePointer: filepath.Join(source, "azure.yaml"),
						modelDeployment: "existing", noPrompt: test.noPrompt,
					}
					var err error
					if templateType == TemplateTypeAzureYaml {
						err = runInitFromAzureYaml(t.Context(), flags, client, http.DefaultClient, content, "")
					} else {
						err = runInitFromAzdTemplate(t.Context(), flags, client, http.DefaultClient,
							&AgentTemplate{Source: source})
					}
					localErr, ok := errors.AsType[*azdext.LocalError](err)
					require.True(t, ok, "expected a selection error, got %v", err)
					require.Equal(t, test.code, localErr.Code)
					require.Zero(t, workflow.runCalls)
					require.Zero(t, env.getValuesCalls)
					require.Empty(t, env.setKeys)
					require.Equal(t, before, env.values["test"])
					require.NoFileExists(t, filepath.Join(target, "azure.yaml"))
					if layout.existingTarget {
						after, err := os.ReadFile(envPath)
						require.NoError(t, err)
						require.Equal(t, originalEnv, after)
					} else {
						require.NoDirExists(t, target)
					}
					wantPrompts := int32(1)
					if test.noPrompt {
						wantPrompts = 0
					}
					require.Equal(t, wantPrompts, prompt.selectCalls.Load())
					cwd, err := os.Getwd()
					require.NoError(t, err)
					require.Equal(t, root, cwd)
				})
			}
		}
	}
}

func TestAdoptedDeploymentPreflightAndApply(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			root := t.TempDir()
			writeAdoptedDeploymentTemplate(t, root, false, []string{"chat", "embed"}[:count]...)
			raw := []any{}
			for _, name := range []string{"chat", "embed"}[:count] {
				raw = append(raw, map[string]any{"name": name})
			}
			server := &helpersProjectServer{project: &azdext.ProjectConfig{
				Path: root, Services: map[string]*azdext.ServiceConfig{
					"project": {Host: project.FoundryProjectHost,
						AdditionalProperties: mustStruct(t, map[string]any{"deployments": raw})},
				},
			}}
			env := &adoptedReplacementEnvironment{promptEnvironmentServer: promptEnvironmentServer{
				values: map[string]string{},
			}}
			prompt := &helpersPromptServer{selectIndex: 1}
			client := newHelpersTestAzdClient(t, server, prompt, env)
			replaced, err := preflightAdoptedDeploymentReplacement(t.Context(), client, root,
				&initFlags{modelDeployment: "existing", noPrompt: count < 2})
			require.NoError(t, err)
			require.Zero(t, env.writes)
			want := []string{"", "chat", "embed"}[count]
			require.Equal(t, want, replaced)
			calls := prompt.selectCalls.Load()
			if count == 2 {
				require.EqualValues(t, 1, calls)
				require.Equal(t, `Which template deployment does "existing" replace?`, prompt.lastSelect.Options.Message)
			} else {
				require.Zero(t, calls)
			}
			// Authoring can change the environment between selection and apply.
			current, err := project.EncodeProjectDeploymentsEnv([]project.Deployment{
				adoptedTestDeployment("chat", "gpt-4o"), adoptedTestDeployment("embed", "embedding"),
				adoptedTestDeployment("authored", "gpt-4o"),
			})
			require.NoError(t, err)
			env.values[projectDeploymentsEnvKey] = current
			require.NoError(t, replaceAdoptedTemplateDeployment(t.Context(), client, "test", "existing", replaced))
			require.Equal(t, calls, prompt.selectCalls.Load())
			require.Equal(t, "existing", env.values["AZURE_AI_MODEL_DEPLOYMENT_NAME"])
			expected := [][]string{{"chat", "embed", "authored"}, {"embed", "authored"}, {"chat", "authored"}}[count]
			require.Equal(t, expected, decodedNames(t, env.values[projectDeploymentsEnvKey]))
			if replaced != "" {
				bindings, err := synthesis.DecodeDeploymentBindings(env.values[synthesis.DeploymentBindingsEnvKey])
				require.NoError(t, err)
				require.Equal(t, map[string]string{replaced: "existing"}, bindings)
			}
		})
	}
}

func TestAdoptedDeploymentPreflightExpandsSelectedNameAtApply(t *testing.T) {
	root := t.TempDir()
	writeAdoptedDeploymentTemplate(t, root, true, "${CHAT_NAME}")
	t.Setenv("CHAT_NAME", "process-chat")
	server := &helpersProjectServer{project: &azdext.ProjectConfig{
		Path: root, Services: map[string]*azdext.ServiceConfig{
			"project": {Host: project.FoundryProjectHost,
				AdditionalProperties: mustStruct(t, map[string]any{"$ref": "./project.yaml"})},
		},
	}}
	env := &adoptedReplacementEnvironment{promptEnvironmentServer: promptEnvironmentServer{
		values: map[string]string{},
	}}
	prompt := &helpersPromptServer{}
	client := newHelpersTestAzdClient(t, server, prompt, env)
	replaced, err := preflightAdoptedDeploymentReplacement(t.Context(), client, root,
		&initFlags{modelDeployment: "existing", noPrompt: true})
	require.NoError(t, err)
	require.Equal(t, "${CHAT_NAME}", replaced)
	require.Zero(t, env.writes)
	env.values["CHAT_NAME"] = "persisted-chat"
	require.NoError(t, replaceAdoptedTemplateDeployment(t.Context(), client, "test", "existing", replaced))
	require.Empty(t, decodedNames(t, env.values[projectDeploymentsEnvKey]))
	bindings, err := synthesis.DecodeDeploymentBindings(env.values[synthesis.DeploymentBindingsEnvKey])
	require.NoError(t, err)
	require.Equal(t, map[string]string{"persisted-chat": "existing"}, bindings)
	require.Zero(t, prompt.selectCalls.Load())
}
