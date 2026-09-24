// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package config_test

import (
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent/consent"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/stretchr/testify/require"
)

func TestCloneConsentConfig(t *testing.T) {
	value := consent.ConsentConfig{
		Rules: []consent.ConsentRule{{
			Scope:      consent.ScopeProject,
			Target:     consent.NewGlobalTarget(),
			Action:     consent.ActionReadOnly,
			Operation:  consent.OperationTypeTool,
			Permission: consent.PermissionAllow,
			GrantedAt:  time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		}},
	}
	original := config.NewConfig(map[string]any{"consent": value})
	cloned, err := config.Clone(original)
	require.NoError(t, err)
	actual, exists := cloned.Get("consent")
	require.True(t, exists)
	require.IsType(t, value, actual)
	require.Equal(t, value, actual)
	actual.(consent.ConsentConfig).Rules[0].Permission = consent.PermissionDeny
	require.Equal(t, consent.PermissionAllow, value.Rules[0].Permission)

	env := environment.New("test")
	require.NoError(t, env.Config().Set("consent", value))
	value.Rules[0].Permission = consent.PermissionPrompt
	actual, exists = env.Config().Get("consent")
	require.True(t, exists)
	require.IsType(t, value, actual)
	require.Equal(t, consent.PermissionAllow, actual.(consent.ConsentConfig).Rules[0].Permission)
	actual.(consent.ConsentConfig).Rules[0].Permission = consent.PermissionDeny
	var section consent.ConsentConfig
	exists, err = env.Config().GetSection("consent", &section)
	require.NoError(t, err)
	require.True(t, exists)
	require.Len(t, section.Rules, 1)
	require.Equal(t, consent.PermissionAllow, section.Rules[0].Permission)
	require.Equal(t, value.Rules[0].GrantedAt, section.Rules[0].GrantedAt)
}
