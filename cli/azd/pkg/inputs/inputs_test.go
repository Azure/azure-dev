// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package inputs_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/inputs"
	"github.com/azure/azure-dev/cli/azd/pkg/ioc"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockinputs"
	"github.com/stretchr/testify/require"
)

func TestCombineInputOptions(t *testing.T) {
	for _, tt := range []struct {
		name        string
		responses   []*v1beta.ResolveInputResponse
		allowed     []string
		want        []string
		recommended []string
		wantErr     string
	}{
		{name: "declines", responses: []*v1beta.ResolveInputResponse{{}}},
		{name: "different suggestions coexist", responses: []*v1beta.ResolveInputResponse{
			{Options: []*v1beta.InputOption{{Value: "eastus2"}}},
			{Options: []*v1beta.InputOption{{Value: "westus3"}}},
		}, want: []string{"eastus2", "westus3"}},
		{name: "required sets intersect before recommendations", responses: []*v1beta.ResolveInputResponse{
			{Required: true, Options: []*v1beta.InputOption{
				{Value: "eastus2"}, {Value: "westus3", Recommended: true},
			}},
			{Required: true, Options: []*v1beta.InputOption{{Value: "eastus2"}}},
		}, want: []string{"eastus2"}},
		{name: "recommendations cannot override required sets", responses: []*v1beta.ResolveInputResponse{
			{Options: []*v1beta.InputOption{{Value: "westus3", Recommended: true}}},
			{Required: true, Options: []*v1beta.InputOption{{Value: "eastus2"}}},
		}, want: []string{"eastus2"}},
		{name: "disjoint required sets", responses: []*v1beta.ResolveInputResponse{
			{Required: true, Options: []*v1beta.InputOption{{Value: "eastus2"}}},
			{Required: true, Options: []*v1beta.InputOption{{Value: "westus3"}}},
		}, wantErr: "no compatible options"},
		{name: "native allowed values constrain required set", responses: []*v1beta.ResolveInputResponse{
			{Required: true, Options: []*v1beta.InputOption{{Value: "eastus2"}, {Value: "westus3"}}},
		}, allowed: []string{"westus3"}, want: []string{"westus3"}},
		{name: "native allowed values conflict", responses: []*v1beta.ResolveInputResponse{
			{Required: true, Options: []*v1beta.InputOption{{Value: "eastus2"}}},
		}, allowed: []string{"westus3"}, wantErr: "no compatible options"},
		{name: "equal options merge preferences and provenance", responses: []*v1beta.ResolveInputResponse{
			{Options: []*v1beta.InputOption{{Value: "eastus2", Rationale: "Available."}}},
			{Options: []*v1beta.InputOption{
				{Value: "eastus2", Recommended: true, Rationale: "Preferred."},
				{Value: "eastus2", Rationale: "Preferred."},
			}},
		}, want: []string{"eastus2"}, recommended: []string{"eastus2"}},
		{name: "empty required set is not a decline", responses: []*v1beta.ResolveInputResponse{
			{Required: true},
		}, wantErr: "empty option set"},
		{name: "empty string is an option", responses: []*v1beta.ResolveInputResponse{
			{Options: []*v1beta.InputOption{{Value: ""}}},
		}, want: []string{""}},
		{name: "legacy value is a recommendation", responses: []*v1beta.ResolveInputResponse{
			{Value: new("eastus2")},
		}, want: []string{"eastus2"}, recommended: []string{"eastus2"}},
		{name: "mixed legacy and options rejected", responses: []*v1beta.ResolveInputResponse{
			{Value: new("eastus2"), Options: []*v1beta.InputOption{{Value: "westus3"}}},
		}, wantErr: "mixed legacy"},
		{name: "malformed option", responses: []*v1beta.ResolveInputResponse{
			{Options: []*v1beta.InputOption{nil}},
		}, wantErr: "invalid input option"},
		{name: "malformed response", responses: []*v1beta.ResolveInputResponse{nil}, wantErr: "invalid input response"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			definition := &v1beta.InputDefinition{Id: "location", AllowedValues: tt.allowed}
			var contributions []inputs.Contribution
			for i, response := range tt.responses {
				contributions = append(contributions, inputs.Contribution{
					Source: fmt.Sprintf("provider-%d", i), Response: response,
				})
			}
			options, err := inputs.Combine(definition, contributions)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, options)
				require.NotContains(t, err.Error(), "eastus2")
				require.NotContains(t, err.Error(), "westus3")
				return
			}
			require.NoError(t, err)
			var values, recommended []string
			for _, option := range options {
				values = append(values, option.Value)
				if option.Recommended {
					recommended = append(recommended, option.Value)
				}
			}
			require.Equal(t, tt.want, values)
			require.Equal(t, tt.recommended, recommended)
			slices.Reverse(contributions)
			reversed, err := inputs.Combine(definition, contributions)
			require.NoError(t, err)
			require.Equal(t, options, reversed, "contribution order must not affect the result")
		})
	}
}

func TestInputOptionSelection(t *testing.T) {
	for _, tt := range []struct {
		name      string
		options   []inputs.Option
		filter    func(string) (bool, error)
		noPrompt  bool
		prompt    bool
		index     int
		promptErr error
		want      *string
		wantErr   string
		wantNote  string
	}{
		{name: "decline retains fallback"},
		{name: "sole option in no-prompt mode", noPrompt: true,
			options:  []inputs.Option{{Value: "eastus2", Sources: []string{"provider-a", "provider-b"}}},
			want:     new("eastus2"),
			wantNote: `Resolved input "location" from extensions: provider-a, provider-b (only valid option).`},
		{name: "unique recommendation in no-prompt mode", noPrompt: true,
			options: []inputs.Option{
				{Value: "eastus2", Sources: []string{"provider-a"}},
				{Value: "westus3", Recommended: true, Sources: []string{"provider-b"}},
			},
			want:     new("westus3"),
			wantNote: `Resolved input "location" from extensions: provider-b (unique recommendation).`},
		{name: "different recommendations prompt", prompt: true, index: 1,
			options: []inputs.Option{
				{Value: "westus3", Recommended: true, Sources: []string{"provider-b"}, Rationales: []string{"Quota."}},
				{Value: "eastus2", Recommended: true, Sources: []string{"provider-a"}},
			}, want: new("westus3")},
		{name: "options without recommendations prompt", prompt: true, index: 1,
			options: []inputs.Option{{Value: "westus3"}, {Value: "eastus2"}}, want: new("westus3")},
		{name: "ambiguous recommendations fail in no-prompt mode", noPrompt: true,
			options: []inputs.Option{{Value: "eastus2", Recommended: true}, {Value: "westus3", Recommended: true}},
			wantErr: "prompt required"},
		{name: "options without recommendations fail in no-prompt mode", noPrompt: true,
			options: []inputs.Option{{Value: "eastus2"}, {Value: "westus3"}}, wantErr: "prompt required"},
		{name: "invalid recommendation is removed before selection", noPrompt: true,
			options: []inputs.Option{
				{Value: "invalid", Recommended: true, Sources: []string{"provider-a"}},
				{Value: "eastus2", Sources: []string{"provider-b"}},
			},
			filter: func(value string) (bool, error) { return value != "invalid", nil }, want: new("eastus2"),
			wantNote: `Resolved input "location" from extensions: provider-b (only valid option).`},
		{name: "no valid options fails instead of fallback",
			options: []inputs.Option{{Value: "invalid"}},
			filter:  func(string) (bool, error) { return false, nil }, wantErr: "no valid input provider options"},
		{name: "validation failure is not skipped",
			options: []inputs.Option{{Value: "eastus2"}, {Value: "westus3"}},
			filter:  func(string) (bool, error) { return false, errors.New("account lookup failed") },
			wantErr: "account lookup failed"},
		{name: "prompt cancellation propagates", prompt: true, promptErr: context.Canceled,
			options: []inputs.Option{{Value: "eastus2"}, {Value: "westus3"}}, wantErr: "context canceled"},
		{name: "invalid selection fails", prompt: true, index: -1,
			options: []inputs.Option{{Value: "eastus2"}, {Value: "westus3"}}, wantErr: "invalid selection"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			mockContext.Console.SetNoPromptMode(tt.noPrompt)
			prompts := 0
			mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
				prompts++
				require.Len(t, options.Options, 2)
				require.Contains(t, options.Options[0], "eastus2")
				if tt.options[0].Recommended && tt.options[1].Recommended {
					require.Contains(t, options.Options[0], "(recommended)")
					require.Contains(t, options.OptionDetails[1], "provider-b")
					require.Contains(t, options.OptionDetails[1], "Quota.")
				}
				return true
			}).RespondFn(func(input.ConsoleOptions) (any, error) { return tt.index, tt.promptErr })
			ioc.RegisterInstance[inputs.Resolver](mockContext.Container, mockinputs.OptionsResolver(
				func(context.Context, *v1beta.InputDefinition) ([]inputs.Option, error) { return tt.options, nil },
			))
			value, err := inputs.Resolve(t.Context(), mockContext.Container, &v1beta.InputDefinition{
				Id: "location", Kind: inputs.AzureLocationKind, EnvironmentVariable: "AZURE_LOCATION",
			}, tt.filter)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.Nil(t, value)
				if tt.noPrompt {
					required, ok := errors.AsType[*input.PromptRequiredError](err)
					require.True(t, ok)
					require.Equal(t, "AZURE_LOCATION", required.Inputs[0].Sources[0].Name)
				}
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, value)
			}
			if tt.prompt {
				require.Equal(t, 1, prompts)
			} else {
				require.Zero(t, prompts)
			}
			notes := slices.DeleteFunc(mockContext.Console.Output(), func(message string) bool {
				return !strings.HasPrefix(message, "Resolved input ")
			})
			if tt.wantNote != "" {
				require.Equal(t, []string{tt.wantNote}, notes)
			} else {
				require.Empty(t, notes)
			}
		})
	}
}
