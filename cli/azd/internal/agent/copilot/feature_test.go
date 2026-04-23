// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestIsFeatureEnabled_Enabled(t *testing.T) {
	// Enable the feature via environment variable
	t.Setenv("AZD_ALPHA_ENABLE_LLM", "true")

	ucm := &mockUCM{cfg: config.NewConfig(nil)}
	mgr := alpha.NewFeaturesManager(ucm)

	err := IsFeatureEnabled(mgr)
	require.NoError(t, err)
}

func TestIsFeatureEnabled_Disabled(t *testing.T) {
	// Ensure the env var is unset so the feature is off
	t.Setenv("AZD_ALPHA_ENABLE_LLM", "false")

	ucm := &mockUCM{cfg: config.NewConfig(nil)}
	mgr := alpha.NewFeaturesManager(ucm)

	err := IsFeatureEnabled(mgr)
	require.Error(t, err)
	require.Contains(t, err.Error(), DisplayTitle)
	require.Contains(t, err.Error(), "not enabled")
}

func TestFeatureCopilotKey(t *testing.T) {
	// Verify the feature key is the backward-compatible "llm"
	require.Equal(t, alpha.FeatureId("llm"), FeatureCopilot)
}

// mockUCM implements config.UserConfigManager for testing.
type mockUCM struct {
	cfg config.Config
}

func (m *mockUCM) Load() (config.Config, error) {
	return m.cfg, nil
}

func (m *mockUCM) Save(_ config.Config) error {
	return nil
}
