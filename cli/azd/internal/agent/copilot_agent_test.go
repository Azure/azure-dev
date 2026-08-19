// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent

import (
	"context"
	"slices"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentcopilot "github.com/azure/azure-dev/cli/azd/internal/agent/copilot"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
)

func TestCopilotAgentPromptModelAndReasoning(t *testing.T) {
	t.Run("ModelWithoutReasoningLevels", func(t *testing.T) {
		args := copilotAgentTestArgs{
			ExpectedModelID:         "model-with-no-reasoning",
			ExpectedReasoningEffort: "",
			Models: []copilot.ModelInfo{
				{
					ID:   "model-with-no-reasoning",
					Name: "Model with no reasoning",
				},
				{
					ID:                        "some random model that's not used",
					Name:                      "some random model that's not used",
					SupportedReasoningEfforts: []string{"low", "medium", "high"},
					DefaultReasoningEffort:    "medium",
				},
			},
		}

		runCopilotAgentTest(t, args)
	})

	t.Run("ModelWithDefaultReasoning_ExplicitChoice", func(t *testing.T) {
		args := copilotAgentTestArgs{
			Models: []copilot.ModelInfo{
				{
					ID:                        "some random model that's not used",
					Name:                      "some random model that's not used",
					SupportedReasoningEfforts: []string{"low", "medium", "high"},
					DefaultReasoningEffort:    "medium",
				},
				{
					ID:                        "model-with-default-reasoning",
					Name:                      "Model with default reasoning",
					SupportedReasoningEfforts: []string{"pistachio", "vanilla", "radish"},
					DefaultReasoningEffort:    "vanilla",
				}},
			ExpectedModelID:         "model-with-default-reasoning",
			ExpectedReasoningEffort: "pistachio",
		}
		runCopilotAgentTest(t, args)
	})

	t.Run("ModelWithDefaultReasoning_TakeDefault", func(t *testing.T) {
		args := copilotAgentTestArgs{
			Models: []copilot.ModelInfo{
				{
					ID:                        "some random model that's not used",
					Name:                      "some random model that's not used",
					SupportedReasoningEfforts: []string{"low", "medium", "high"},
					DefaultReasoningEffort:    "medium",
				},
				{
					ID:                        "model-with-default-reasoning",
					Name:                      "Model with default reasoning",
					SupportedReasoningEfforts: []string{"pistachio", "vanilla", "radish"},
					DefaultReasoningEffort:    "vanilla",
				}},
			ExpectedModelID:         "model-with-default-reasoning",
			ExpectedReasoningEffort: "vanilla",
		}
		runCopilotAgentTest(t, args)
	})

	t.Run("ModelWithoutDefaultReasoning", func(t *testing.T) {
		args := copilotAgentTestArgs{
			Models: []copilot.ModelInfo{{
				ID:                        "model-with-no-default-reasoning",
				Name:                      "Model with no default reasoning",
				SupportedReasoningEfforts: []string{"low", "medium", "high"},
			}},
			ExpectedModelID:         "model-with-no-default-reasoning",
			ExpectedReasoningEffort: "medium",
		}

		runCopilotAgentTest(t, args)

		// quick check with an even number of reasoning efforts...
		args = copilotAgentTestArgs{
			Models: []copilot.ModelInfo{{
				ID:                        "model-with-no-default-reasoning",
				Name:                      "Model with no default reasoning",
				SupportedReasoningEfforts: []string{"radish", "summer squash", "pumpkin", "blueberry"},
			}},
			ExpectedModelID:         "model-with-no-default-reasoning",
			ExpectedReasoningEffort: "summer squash",
		}

		runCopilotAgentTest(t, args)
	})
}

type copilotAgentTestArgs struct {
	Models                  []copilot.ModelInfo
	ExpectedModelID         string
	ExpectedReasoningEffort string
}

func runCopilotAgentTest(
	t *testing.T, args copilotAgentTestArgs,
) {
	var agent *CopilotAgent
	var configManager config.UserConfigManager
	{
		modelIdx := -1
		var model copilot.ModelInfo

		// before they're presented, the model names are sorted by Name so they
		// group properly (we need to do the same here to pick the right idx)
		slices.SortFunc(args.Models, func(a, b copilot.ModelInfo) int {
			return strings.Compare(a.Name, b.Name)
		})

		for i, m := range args.Models {
			if m.ID == args.ExpectedModelID {
				// small hack - the models are shifted by 1, since the first model in the
				// select-list is "Default model", which basically bypasses any configuration
				// and let's copilot choose.
				modelIdx = i + 1
				model = m
				break
			}
		}

		require.NotEqual(t, -1, modelIdx, "model ID you passed for the test isn't in your list of models")

		mockContext := mocks.NewMockContext(t.Context())
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return options.Message == selectAIModelMessage
		}).Respond(modelIdx)

		if len(model.SupportedReasoningEfforts) > 0 {
			// find the right reasoning index
			reasoningIdx := -1

			for i, re := range model.SupportedReasoningEfforts {
				if re == args.ExpectedReasoningEffort {
					reasoningIdx = i
					break
				}
			}

			require.NotEqualf(t, -1, reasoningIdx,
				"reasoning you passed for the test isn't in the reasoning list for your model %s", model.ID)

			mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
				if options.Message != selectReasoningEffortLevelMessage {
					return false
				}

				if model.DefaultReasoningEffort == "" {
					// if there's no default value in the model we don't tag a specific level, the default
					// is just the lowest reasoning level, but it's not listed as 'recommended'.
					return options.DefaultValue == model.SupportedReasoningEfforts[len(model.SupportedReasoningEfforts)/2]
				} else if options.DefaultValue != model.DefaultReasoningEffort+" (recommended)" {
					return false
				}

				return true
			}).Respond(reasoningIdx)
		}

		configManager = config.NewUserConfigManager(mockContext.ConfigManager)

		agent = &CopilotAgent{
			console:       mockContext.Console,
			configManager: configManager,
			listModels: func(context.Context) ([]copilot.ModelInfo, error) {
				return args.Models, nil
			},
		}
	}

	modelID, reasoningEffort, err := agent.promptModelAndReasoning(t.Context(), &initOptions{})
	require.NoError(t, err)

	require.Equal(t, args.ExpectedModelID, modelID)
	require.Equal(t, args.ExpectedReasoningEffort, reasoningEffort)

	// quick check -what we're returning here matches what's stored in the configuration
	userConfig, err := configManager.Load()
	require.NoError(t, err)

	storedModelID, hasModel := userConfig.GetString(agentcopilot.ConfigKeyModel)
	require.True(t, hasModel)
	assert.Equal(t, modelID, storedModelID)

	if args.ExpectedReasoningEffort == "" {
		storedReasoningEffort, hasEffort := userConfig.GetString(agentcopilot.ConfigKeyReasoningEffort)
		require.True(t, hasEffort)
		assert.Equal(t, "", storedReasoningEffort)
	} else {
		storedReasoningEffort, hasEffort := userConfig.GetString(agentcopilot.ConfigKeyReasoningEffort)
		require.True(t, hasEffort)
		assert.Equal(t, args.ExpectedReasoningEffort, storedReasoningEffort)
	}
}
