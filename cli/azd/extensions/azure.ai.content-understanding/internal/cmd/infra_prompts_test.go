// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/cuclient"
	"azure.ai.content-understanding/internal/infra"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInfraGenerateContextPrecedence(t *testing.T) {
	for _, test := range []struct {
		name, inherited string
		args            []string
		prompts         generationPrompts
		environment     string
		location        string
	}{
		{"omitted", "", nil, generationPrompts{true, true, true, true}, "dev", "eastus2"},
		{"inherited", "staging", nil, generationPrompts{false, true, true, true}, "staging", "eastus2"},
		{"explicit", "other", []string{"-e", "target", "-l", "westus3", "--foundry-prefix", "app",
			"--assign-roles=false"}, generationPrompts{}, "target", "westus3"},
		{"existing", "", []string{"--foundry-endpoint", "foundry"}, generationPrompts{environment: true}, "dev", "westus3"},
		{"no roles false", "", []string{"--no-assign-roles=false"},
			generationPrompts{true, true, true, false}, "dev", "eastus2"},
		{"empty prefix", "", []string{"--foundry-prefix", ""},
			generationPrompts{true, true, false, true}, "dev", "eastus2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			host := infraCommandFixture(t)
			t.Setenv("AZD_ENVIRONMENT", test.inherited)
			host.prompt = func(choices infra.Choices) (infra.Choices, error) {
				assert.Equal(t, test.environment, choices.Environment)
				assert.Equal(t, test.location, choices.Location)
				if test.prompts.environment {
					choices.Environment = "chosen"
				}
				return choices, nil
			}
			target := filepath.Join(testutil.TempDir(t), "project")
			args := append([]string{"infra", "generate", "-d", target, "--models", "none"}, test.args...)
			stdout, stderr, err := executeInfra(t, host, args...)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Equal(t, []generationPrompts{test.prompts}, host.prompts)
			environment := test.environment
			if test.prompts.environment {
				environment = "chosen"
			}
			assert.Contains(t, stdout, "Environment: "+environment)
			// #nosec G304 -- Reads the generated test-owned environment.
			body, err := os.ReadFile(filepath.Join(target, ".azure", environment, ".env"))
			require.NoError(t, err)
			assert.Contains(t, string(body), `AZURE_ENV_NAME="`+environment+`"`)
			assert.NotContains(t, host.calls, "snapshot")
		})
	}
}

func TestInfraGenerateExplicitInputsAndNoPrompt(t *testing.T) {
	for _, flag := range []string{"--environment", "--location", "--models", "--api-version",
		"--foundry-endpoint", "--output-dir"} {
		t.Run(flag, func(t *testing.T) {
			host := infraCommandFixture(t)
			target := filepath.Join(testutil.TempDir(t), "project")
			stdout, _, err := executeInfra(t, host, "infra", "generate", "-d", target, flag, "")
			require.Error(t, err)
			if flag != "--api-version" {
				assert.Contains(t, err.Error(), flag)
			}
			assert.Empty(t, stdout)
			assert.Empty(t, host.calls)
			assert.NoDirExists(t, target)
		})
	}
	host := infraCommandFixture(t)
	stdout, _, err := executeInfra(t, host, "infra", "generate",
		"--foundry-endpoint", "foundry", "--foundry-prefix", "")
	require.Error(t, err)
	assert.Empty(t, stdout)
	assert.Empty(t, host.calls)
}

func TestInfraGeneratePromptCancellation(t *testing.T) {
	host := infraCommandFixture(t)
	host.prompt = func(choices infra.Choices) (infra.Choices, error) { return choices, context.Canceled }
	target := filepath.Join(testutil.TempDir(t), "project")
	stdout, _, err := executeInfra(t, host, "infra", "generate", "-d", target, "--models", "none")
	require.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, stdout)
	assert.NoDirExists(t, target)
	assert.Equal(t, "close", host.calls[len(host.calls)-1])
}

func TestInfraGenerateOutputModes(t *testing.T) {
	for _, format := range []string{"default", "json", "none"} {
		t.Run(format, func(t *testing.T) {
			host := infraCommandFixture(t)
			t.Setenv("AZD_ENVIRONMENT", "context")
			target := filepath.Join(testutil.TempDir(t), "project's $name [1]")
			args := []string{"infra", "generate", "-d", target, "--models", "none", "--no-prompt", "-o", format}
			stdout, stderr, err := executeInfra(t, host, args...)
			require.NoError(t, err)
			assert.Empty(t, stderr)
			assert.Empty(t, host.prompts)
			switch format {
			case "json":
				var result map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(stdout), &result))
				assert.Len(t, result, 16)
				for _, key := range []string{"environment", "location", "api_version", "foundry_prefix",
					"foundry_endpoint", "foundry_resource_group", "model_selection", "assign_roles",
					"force_configuration_setup", "subscriptionId", "subscriptionName", "tenantId",
					"outputDirectory", "files", "reusedExistingTemplate", "nextSteps"} {
					assert.Contains(t, result, key)
				}
				var generation infra.Generation
				require.NoError(t, json.Unmarshal([]byte(stdout), &generation))
				assert.Equal(t, "context", generation.Environment)
				assert.Equal(t, "eastus2", generation.Location)
				assert.Equal(t, "none", generation.ModelSelection)
				assert.False(t, generation.AssignRoles)
				assert.Len(t, generation.Files, 11)
				assert.Len(t, generation.NextSteps, 3)
			case "none":
				assert.Empty(t, stdout)
			default:
				assert.Contains(t, stdout, "Generated project: "+target)
				assert.Contains(t, stdout, "No Azure resources were deployed.")
				if runtime.GOOS == "windows" {
					assert.Contains(t, stdout, "Set-Location -LiteralPath '"+
						strings.ReplaceAll(target, "'", "''")+"'")
				} else {
					assert.Contains(t, stdout, `project'"'"'s $name [1]'`)
				}
				stdout, _, err = executeInfra(t, host, args...)
				require.NoError(t, err)
				assert.Contains(t, stdout, "Updated project:")
				assert.Contains(t, stdout, "preserved infra/models.json")
			}
			assert.FileExists(t, filepath.Join(target, "azure.yaml"))
		})
	}
	t.Run("JSON skips new wizard without no-prompt", func(t *testing.T) {
		host := infraCommandFixture(t)
		_, _, err := executeInfra(t, host, "infra", "generate", "-d",
			filepath.Join(testutil.TempDir(t), "project"), "--models", "none", "-o", "json")
		require.NoError(t, err)
		assert.Empty(t, host.prompts)
	})
}

func generationPromptFixture(t *testing.T) (*infraRPC, *azdInfraHost, infra.Choices) {
	t.Helper()
	rpc := &infraRPC{}
	rpc.promptText = func(request *azdext.PromptRequest) (*azdext.PromptResponse, error) {
		return &azdext.PromptResponse{Value: request.GetOptions().GetDefaultValue()}, nil
	}
	rpc.selectOption = func(request *azdext.SelectRequest) (*azdext.SelectResponse, error) {
		return &azdext.SelectResponse{Value: request.GetOptions().SelectedIndex}, nil
	}
	rpc.confirm = func(request *azdext.ConfirmRequest) (*azdext.ConfirmResponse, error) {
		assert.False(t, request.GetOptions().GetDefaultValue())
		assert.NotContains(t, request.GetOptions().GetHelpMessage(), "API key")
		return &azdext.ConfirmResponse{Value: new(false)}, nil
	}
	return rpc, infraRPCClient(t, rpc), infra.Choices{
		Environment: "dev", Location: "eastus2", APIVersion: "2025-11-01", ModelSelection: "none",
	}
}

func TestInfraGenerationHostPrompts(t *testing.T) {
	rpc, host, choices := generationPromptFixture(t)
	inputs := []string{"../invalid", "chosen", "invalid prefix", "app"}
	rpc.promptText = func(request *azdext.PromptRequest) (*azdext.PromptResponse, error) {
		if len(inputs) == 3 || len(inputs) == 1 {
			assert.Contains(t, request.GetOptions().GetHelpMessage(), "Invalid input:")
		}
		value := inputs[0]
		inputs = inputs[1:]
		return &azdext.PromptResponse{Value: value}, nil
	}
	rpc.selectOption = func(request *azdext.SelectRequest) (*azdext.SelectResponse, error) {
		options := request.GetOptions()
		assert.Equal(t, "eastus2", options.Choices[options.GetSelectedIndex()].GetValue())
		assert.Len(t, options.Choices, len(infra.Regions))
		assert.Equal(t, "westus3", options.Choices[11].GetValue())
		return &azdext.SelectResponse{Value: new(int32(11))}, nil
	}
	result, err := host.PromptGeneration(azdext.WithAccessToken(t.Context()), choices,
		generationPrompts{true, true, true, true})
	require.NoError(t, err)
	assert.Empty(t, inputs)
	assert.Equal(t, "chosen", result.Environment)
	assert.Equal(t, "app", result.FoundryPrefix)
	assert.Equal(t, "westus3", result.Location)
	assert.False(t, result.AssignRoles)
}

func TestInfraGenerationHostDoesNotRetryOtherFields(t *testing.T) {
	for _, test := range []struct {
		name, environment, location, prefix, code string
		prompts                                   generationPrompts
	}{
		{"environment with invalid location", "dev", "francecentral", "", "invalid_location",
			generationPrompts{environment: true}},
		{"prefix with invalid location", "dev", "francecentral", "", "invalid_location",
			generationPrompts{prefix: true}},
		{"environment with invalid prefix", "dev", "eastus2", "invalid prefix", "invalid_foundry_prefix",
			generationPrompts{environment: true}},
		{"prefix with invalid environment", "../invalid", "eastus2", "", "invalid_environment",
			generationPrompts{prefix: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rpc, host, choices := generationPromptFixture(t)
			choices.Environment, choices.Location, choices.FoundryPrefix = test.environment, test.location, test.prefix
			calls := 0
			rpc.promptText = func(request *azdext.PromptRequest) (*azdext.PromptResponse, error) {
				calls++
				if calls > 1 {
					return nil, status.Error(codes.FailedPrecondition, "unexpected repeated prompt")
				}
				return &azdext.PromptResponse{Value: request.GetOptions().GetDefaultValue()}, nil
			}
			_, err := host.PromptGeneration(azdext.WithAccessToken(t.Context()), choices, test.prompts)
			require.Error(t, err)
			local, ok := errors.AsType[*cuclient.Error](err)
			require.True(t, ok, "expected the unrelated validation error, got %v", err)
			assert.Equal(t, test.code, local.Code)
			assert.Equal(t, 1, calls)
			rpc.mu.Lock()
			assert.Empty(t, rpc.writes)
			rpc.mu.Unlock()
		})
	}
}

func TestInfraGenerationHostPromptErrors(t *testing.T) {
	for _, stage := range []string{"environment", "prefix", "region", "roles"} {
		for _, invalid := range []bool{false, true} {
			if invalid && (stage == "environment" || stage == "prefix") {
				continue
			}
			t.Run(stage+map[bool]string{false: "/canceled", true: "/invalid"}[invalid], func(t *testing.T) {
				rpc, host, choices := generationPromptFixture(t)
				prompts := generationPrompts{}
				var failure error
				if !invalid {
					failure = status.Error(codes.Canceled, "canceled")
				}
				switch stage {
				case "environment", "prefix":
					prompts.environment, prompts.prefix = stage == "environment", stage == "prefix"
					rpc.promptText = func(*azdext.PromptRequest) (*azdext.PromptResponse, error) {
						return nil, failure
					}
				case "region":
					prompts.location = true
					rpc.selectOption = func(*azdext.SelectRequest) (*azdext.SelectResponse, error) {
						if invalid {
							return &azdext.SelectResponse{Value: new(int32(-1))}, nil
						}
						return nil, failure
					}
				case "roles":
					prompts.roles = true
					rpc.confirm = func(*azdext.ConfirmRequest) (*azdext.ConfirmResponse, error) {
						return nil, failure
					}
				}
				_, err := host.PromptGeneration(azdext.WithAccessToken(t.Context()), choices, prompts)
				require.Error(t, err)
				rpc.mu.Lock()
				assert.Empty(t, rpc.writes)
				rpc.mu.Unlock()
			})
		}
	}
}

func pickerModels() []infra.Model {
	return []infra.Model{
		{Name: "gpt-5.2", Model: "gpt-5.2", Version: "1", Kind: "completion", SKU: "GlobalStandard", Default: true},
		{Name: "gpt-5.2", Model: "gpt-5.2", Version: "2", Kind: "completion", SKU: "GlobalStandard"},
		{Name: "text-embedding-3-large", Model: "text-embedding-3-large", Version: "1",
			Kind: "embedding", SKU: "Standard", Default: true},
	}
}

func TestInfraModelPickerSelections(t *testing.T) {
	for _, scenario := range []string{"recommended", "none", "correct conflict", "ambiguous recommendation"} {
		t.Run(scenario, func(t *testing.T) {
			rpc := &infraRPC{confirm: func(request *azdext.ConfirmRequest) (*azdext.ConfirmResponse, error) {
				assert.False(t, request.GetOptions().GetDefaultValue())
				assert.Contains(t, request.GetOptions().GetHelpMessage(), "charges")
				return &azdext.ConfirmResponse{Value: new(true)}, nil
			}}
			models := pickerModels()
			if scenario == "ambiguous recommendation" {
				models[0].Default = false
			}
			calls := 0
			rpc.multiSelect = func(request *azdext.MultiSelectRequest) (*azdext.MultiSelectResponse, error) {
				calls++
				options := request.GetOptions()
				assert.True(t, options.GetAllowEmptySelection())
				assert.Contains(t, options.Choices[0].GetLabel(), "gpt-5.2@1 | completion | GlobalStandard")
				if scenario == "ambiguous recommendation" {
					assert.Contains(t, options.GetHelpMessage(), "No models preselected:")
					for _, choice := range options.Choices {
						assert.False(t, choice.GetSelected())
					}
					return &azdext.MultiSelectResponse{}, nil
				}
				if scenario == "none" {
					return &azdext.MultiSelectResponse{}, nil
				}
				if scenario == "correct conflict" {
					if calls == 1 {
						return &azdext.MultiSelectResponse{Values: options.Choices[:2]}, nil
					}
					assert.Contains(t, options.GetHelpMessage(), "Invalid selection:")
					return &azdext.MultiSelectResponse{Values: []*azdext.MultiSelectChoice{options.Choices[1]}}, nil
				}
				assert.True(t, options.Choices[0].GetSelected())
				assert.False(t, options.Choices[1].GetSelected())
				assert.True(t, options.Choices[2].GetSelected())
				assert.Contains(t, options.Choices[0].GetLabel(), "(recommended)")
				return &azdext.MultiSelectResponse{Values: []*azdext.MultiSelectChoice{
					options.Choices[0], options.Choices[2],
				}}, nil
			}
			host := infraRPCClient(t, rpc)
			capabilities := &infraCapabilities{host: host}
			selected, err := capabilities.PromptModels(azdext.WithAccessToken(t.Context()), models)
			require.NoError(t, err)
			rpc.mu.Lock()
			if scenario == "none" || scenario == "ambiguous recommendation" {
				assert.NotContains(t, rpc.calls, "confirm")
			} else {
				assert.Contains(t, rpc.calls, "confirm")
			}
			rpc.mu.Unlock()
			switch scenario {
			case "recommended":
				assert.Equal(t, []infra.Model{models[0], models[2]}, selected)
			case "correct conflict":
				assert.Equal(t, []infra.Model{models[1]}, selected)
				assert.Equal(t, 2, calls)
			default:
				assert.Empty(t, selected)
			}
		})
	}
}

func TestInfraModelPickerCancellation(t *testing.T) {
	rpc := &infraRPC{multiSelect: func(*azdext.MultiSelectRequest) (*azdext.MultiSelectResponse, error) {
		return nil, status.Error(codes.Canceled, "canceled")
	}}
	host := infraRPCClient(t, rpc)
	capabilities := &infraCapabilities{host: host}
	selected, err := capabilities.PromptModels(azdext.WithAccessToken(t.Context()), pickerModels())
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err))
	assert.Nil(t, selected)
	host.noPrompt = true
	_, err = capabilities.PromptModels(t.Context(), pickerModels())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "interactive decision")
}

func TestInfraModelPickerInvalidResponse(t *testing.T) {
	calls := 0
	rpc := &infraRPC{multiSelect: func(request *azdext.MultiSelectRequest) (*azdext.MultiSelectResponse, error) {
		calls++
		if calls == 1 {
			return &azdext.MultiSelectResponse{Values: []*azdext.MultiSelectChoice{{Value: "unknown@1"}}}, nil
		}
		assert.Contains(t, request.GetOptions().GetHelpMessage(), "Invalid selection:")
		return nil, status.Error(codes.Canceled, "canceled")
	}}
	host := infraRPCClient(t, rpc)
	_, err := host.promptModels(azdext.WithAccessToken(t.Context()), pickerModels())
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err))
	assert.Equal(t, 2, calls)
}

func TestInfraGenerateCanceledContext(t *testing.T) {
	_, host, choices := generationPromptFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := host.PromptGeneration(ctx, choices, generationPrompts{true, true, true, true})
	assert.True(t, errors.Is(err, context.Canceled))
}

type infraPickerCapabilities struct {
	*infraCommandCapabilities
	host *azdInfraHost
}

func (c *infraPickerCapabilities) PromptModels(ctx context.Context, models []infra.Model) ([]infra.Model, error) {
	return c.host.promptModels(ctx, models)
}

func TestInfraModelPickerCancellationPreventsMutations(t *testing.T) {
	for _, scenario := range []string{
		"picker canceled", "confirmation default", "confirmation canceled", "missing decision",
	} {
		t.Run(scenario, func(t *testing.T) {
			rpc := &infraRPC{
				multiSelect: func(request *azdext.MultiSelectRequest) (*azdext.MultiSelectResponse, error) {
					if scenario == "picker canceled" {
						return nil, status.Error(codes.Canceled, "canceled")
					}
					return &azdext.MultiSelectResponse{Values: request.GetOptions().GetChoices()}, nil
				},
				confirm: func(request *azdext.ConfirmRequest) (*azdext.ConfirmResponse, error) {
					assert.False(t, request.GetOptions().GetDefaultValue())
					switch scenario {
					case "confirmation canceled":
						return nil, status.Error(codes.Canceled, "canceled")
					case "missing decision":
						return &azdext.ConfirmResponse{}, nil
					default:
						return &azdext.ConfirmResponse{Value: request.GetOptions().DefaultValue}, nil
					}
				},
			}
			capabilities := &infraPickerCapabilities{
				infraCommandCapabilities: &infraCommandCapabilities{}, host: infraRPCClient(t, rpc),
			}
			output := filepath.Join(testutil.TempDir(t), "models.json")
			_, err := infra.SetupModels(azdext.WithAccessToken(t.Context()), capabilities, "prompt", output, true, true)
			require.Error(t, err)
			if scenario == "confirmation default" {
				assert.ErrorIs(t, err, context.Canceled)
			}
			assert.Equal(t, []string{"supported", "catalog"}, capabilities.calls)
			assert.NoFileExists(t, output)
		})
	}
}

func TestInfraGenerateOutputFailure(t *testing.T) {
	host := infraCommandFixture(t)
	root := newRootCommandWithInfra(nil, func(*cobra.Command, *azdext.ExtensionContext) (infraHost, error) {
		return host, nil
	})
	target := filepath.Join(testutil.TempDir(t), "project")
	root.SetOut(failingWriter{})
	root.SetArgs([]string{"infra", "generate", "-d", target, "--models", "none", "--no-prompt"})
	err := root.ExecuteContext(t.Context())
	require.Error(t, err)
	assert.FileExists(t, filepath.Join(target, "azure.yaml"))
}

func TestInfraGenerationThroughHostRPC(t *testing.T) {
	for _, stage := range []string{"success", "environment", "prefix", "region", "roles"} {
		t.Run(stage, func(t *testing.T) {
			_ = infraCommandFixture(t)
			rpc, _, _ := generationPromptFixture(t)
			rpc.subscriptions = []*azdext.Subscription{{Id: "sub", TenantId: "tenant", Name: "Subscription"}}
			rpc.defaultEnvironment = "unrelated"
			rpc.promptText = func(request *azdext.PromptRequest) (*azdext.PromptResponse, error) {
				current, value := "prefix", "app"
				if request.GetOptions().GetRequired() {
					current, value = "environment", "chosen"
				}
				if stage == current {
					return nil, status.Error(codes.Canceled, "canceled")
				}
				return &azdext.PromptResponse{Value: value}, nil
			}
			rpc.selectOption = func(request *azdext.SelectRequest) (*azdext.SelectResponse, error) {
				if stage == "region" {
					return nil, status.Error(codes.Canceled, "canceled")
				}
				return &azdext.SelectResponse{Value: request.GetOptions().SelectedIndex}, nil
			}
			rpc.confirm = func(*azdext.ConfirmRequest) (*azdext.ConfirmResponse, error) {
				if stage == "roles" {
					return nil, status.Error(codes.Canceled, "canceled")
				}
				return &azdext.ConfirmResponse{Value: new(true)}, nil
			}
			target := filepath.Join(testutil.TempDir(t), "project")
			stdout, stderr, err := executeRoot(t, "infra", "generate", "-d", target, "--models", "none")
			assert.Empty(t, stderr)
			if stage == "success" {
				require.NoError(t, err)
				assert.Contains(t, stdout, "Environment: chosen")
				// #nosec G304 -- Reads a generated test-owned environment.
				body, err := os.ReadFile(filepath.Join(target, "infra", "main.parameters.json"))
				require.NoError(t, err)
				var document struct {
					Parameters map[string]json.RawMessage `json:"parameters"`
				}
				require.NoError(t, json.Unmarshal(body, &document))
				assert.JSONEq(t, `{"value":"app"}`, string(document.Parameters["foundryResourcePrefix"]))
				assert.JSONEq(t, `{"value":"true"}`, string(document.Parameters["assignRolesToPrincipal"]))
				assert.NoDirExists(t, filepath.Join(target, ".azure", "unrelated"))
			} else {
				require.Error(t, err)
				assert.Empty(t, stdout)
				assert.NoDirExists(t, target)
			}
			rpc.mu.Lock()
			assert.Empty(t, rpc.writes)
			assert.NotContains(t, rpc.calls, "current")
			rpc.mu.Unlock()
		})
	}
}
