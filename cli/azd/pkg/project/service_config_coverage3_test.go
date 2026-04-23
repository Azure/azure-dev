// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ServiceConfig_Path_Relative_Coverage3(t *testing.T) {
	sc := &ServiceConfig{
		RelativePath: "src/web",
		Project:      &ProjectConfig{Path: "/my/project"},
	}

	path := sc.Path()
	assert.Contains(t, path, "src")
	assert.Contains(t, path, "web")
}

func Test_ServiceConfig_Path_Absolute_Coverage3(t *testing.T) {
	dir := t.TempDir()
	sc := &ServiceConfig{
		RelativePath: dir,
		Project:      &ProjectConfig{Path: "/my/project"},
	}

	assert.Equal(t, dir, sc.Path())
}

func Test_IsConditionTrue(t *testing.T) {
	tests := []struct {
		value  string
		expect bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"yes", true},
		{"YES", true},
		{"Yes", true},
		{"0", false},
		{"false", false},
		{"FALSE", false},
		{"no", false},
		{"", false},
		{"random", false},
		{"2", false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			assert.Equal(t, tt.expect, isConditionTrue(tt.value))
		})
	}
}

func Test_ServiceConfig_IsEnabled(t *testing.T) {
	t.Run("no condition always enabled", func(t *testing.T) {
		sc := &ServiceConfig{}
		enabled, err := sc.IsEnabled(func(string) string { return "" })
		require.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("condition evaluates to true", func(t *testing.T) {
		sc := &ServiceConfig{
			Condition: osutil.NewExpandableString("${DEPLOY_WEB}"),
		}
		enabled, err := sc.IsEnabled(func(key string) string {
			if key == "DEPLOY_WEB" {
				return "true"
			}
			return ""
		})
		require.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("condition evaluates to false", func(t *testing.T) {
		sc := &ServiceConfig{
			Condition: osutil.NewExpandableString("${DEPLOY_WEB}"),
		}
		enabled, err := sc.IsEnabled(func(key string) string {
			if key == "DEPLOY_WEB" {
				return "false"
			}
			return ""
		})
		require.NoError(t, err)
		assert.False(t, enabled)
	})

	t.Run("condition with literal true", func(t *testing.T) {
		sc := &ServiceConfig{
			Condition: osutil.NewExpandableString("1"),
		}
		enabled, err := sc.IsEnabled(func(string) string { return "" })
		require.NoError(t, err)
		assert.True(t, enabled)
	})
}
