// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package infra

import (
	"context"
	"errors"
	"strings"
	"testing"

	"azure.ai.content-understanding/internal/defaults"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type postFake struct {
	calls       []string
	fail        string
	populated   bool
	cancel      context.CancelFunc
	deployments []Deployment
	mappings    map[string]string
}

func postFixture() *postFake {
	return &postFake{deployments: []Deployment{
		{Name: "completion-deployment", Model: "gpt-5.2", State: "Succeeded"},
		{Name: "embedding-deployment", Model: "text-embedding-3-large", State: "Succeeded"},
	}}
}
func (f *postFake) record(name string) error {
	f.calls = append(f.calls, name)
	if name == f.fail {
		if f.cancel != nil {
			f.cancel()
		}
		return errors.New("private-token-diagnostic")
	}
	return nil
}
func (f *postFake) SetupModels(context.Context) error  { return f.record("models") }
func (f *postFake) MarkComplete(context.Context) error { return f.record("complete") }
func (f *postFake) HasConfiguration(context.Context) (bool, error) {
	return f.populated, f.record("inspect")
}
func (f *postFake) SetConfiguration(_ context.Context, key, value string) error {
	return f.record("set:" + key)
}
func (f *postFake) ListDeployments(context.Context) ([]Deployment, error) {
	return f.deployments, f.record("list")
}
func (f *postFake) ConfigureDefaults(_ context.Context, mappings map[string]string) error {
	f.mappings = mappings
	return f.record("defaults")
}

func TestPostprovisionOrderedReadinessAndAliases(t *testing.T) {
	f := postFixture()
	result, err := Postprovision(t.Context(), PostRequest{Endpoint: "https://account.example"}, f)
	require.NoError(t, err)
	assert.True(t, result.Succeeded)
	assert.True(t, result.EnvironmentConfigured)
	assert.True(t, result.GenerativeReady)
	assert.True(t, result.DefaultsConfigured)
	assert.Empty(t, result.Warnings)
	assert.Equal(t, "completed", result.ModelSetup)
	assert.Equal(t, []string{"models", "complete", "inspect", "set:endpoint", "set:default_analyzer", "list"}, f.calls[:6])
	assert.Equal(t, "completion-deployment", f.mappings[defaults.Completion])
	assert.Equal(t, "completion-deployment", f.mappings[defaults.CompletionMini])
	assert.Equal(t, "embedding-deployment", f.mappings[defaults.Embedding])
	assert.Equal(t, "defaults", f.calls[len(f.calls)-1])
}

func TestPostprovisionPreserveDisableForceAndFailurePolicy(t *testing.T) {
	for _, scenario := range []struct {
		name                                              string
		request                                           PostRequest
		fail                                              string
		populated                                         bool
		succeeded, preserved, disabled, configured, ready bool
	}{
		{"skip-models", PostRequest{SetupComplete: true}, "", false, true, false, false, true, true},
		{"disable", PostRequest{DisableConfiguration: true}, "", false, true, false, true, false, false},
		{"preserve", PostRequest{}, "", true, true, true, false, false, false},
		{"force", PostRequest{ForceConfiguration: true}, "", true, true, false, false, true, true},
		{"optional-models", PostRequest{}, "models", false, true, false, false, true, true},
		{"optional-marker", PostRequest{}, "complete", false, true, false, false, true, true},
		{"inspect-failure", PostRequest{}, "inspect", false, false, false, false, false, false},
		{"required-endpoint", PostRequest{}, "set:endpoint", false, false, false, false, false, true},
		{"required-analyzer", PostRequest{}, "set:default_analyzer", false, false, false, false, false, true},
		{"optional-list", PostRequest{}, "list", false, true, false, false, true, false},
		{"optional-mapping", PostRequest{}, "set:model_deployments.gpt-5.2", false, true, false, false, true, true},
		{"optional-defaults", PostRequest{}, "defaults", false, true, false, false, true, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := postFixture()
			f.fail, f.populated = scenario.fail, scenario.populated
			result, err := Postprovision(t.Context(), scenario.request, f)
			require.NoError(t, err)
			assert.Equal(t, scenario.succeeded, result.Succeeded)
			assert.Equal(t, scenario.preserved, result.EnvironmentPreserved)
			assert.Equal(t, scenario.disabled, result.ConfigurationSetupDisabled)
			assert.Equal(t, scenario.configured, result.EnvironmentConfigured)
			assert.Equal(t, scenario.ready, result.GenerativeReady)
			assert.NotContains(t, strings.Join(result.Messages, " "), "private-token")
			if scenario.request.SetupComplete {
				assert.NotContains(t, f.calls, "models")
			}
			if scenario.preserved || scenario.disabled {
				assert.NotContains(t, f.calls, "set:endpoint")
				assert.NotContains(t, f.calls, "list")
			}
			if scenario.fail == "models" {
				assert.NotContains(t, f.calls, "complete")
			}
		})
	}
	for _, state := range []string{"Failed", "Creating", ""} {
		f := postFixture()
		f.deployments[0].State = state
		result, err := Postprovision(t.Context(), PostRequest{}, f)
		require.NoError(t, err)
		assert.False(t, result.GenerativeReady)
	}
}

func TestPostprovisionCancellationNeverBecomesOptionalSuccess(t *testing.T) {
	for _, step := range []string{"models", "complete", "inspect", "set:endpoint", "list", "defaults"} {
		ctx, cancel := context.WithCancel(t.Context())
		f := postFixture()
		f.fail, f.cancel = step, cancel
		_, err := Postprovision(ctx, PostRequest{}, f)
		require.ErrorIs(t, err, context.Canceled, step)
		cancel()
	}
}
