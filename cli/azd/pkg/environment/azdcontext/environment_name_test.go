// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdcontext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentNameValidation(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"dev", true},
		{"dev.local", true},
		{".hidden", true},
		{"C()mPl3x_ExAmPl3-ThatIsVeryLong", true},
		{strings.Repeat("a", 64), true},
		{"", false},
		{".", false},
		{"..", false},
		{"...", false},
		{"../../trusted-project/.azure/prod", false},
		{`..\..\trusted-project\.azure\prod`, false},
		{"dev/../prod", false},
		{`dev\prod`, false},
		{"/tmp/prod", false},
		{`C:\prod`, false},
		{`C:prod`, false},
		{`\\server\share\prod`, false},
		{"no spaces", false},
		{"no*allowed", false},
		{"dev\x00", false},
		{strings.Repeat("a", 65), false},
		{"NUL", runtime.GOOS != "windows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projectDir, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			ctx := NewAzdContextWithDirectory(projectDir)
			require.Equal(t, tt.valid, IsValidEnvironmentName(tt.name))

			root, err := ctx.EnvironmentRoot(tt.name)
			workDir, workErr := ctx.GetEnvironmentWorkDirectory(tt.name)
			if tt.valid {
				require.NoError(t, err)
				require.NoError(t, workErr)
				require.Equal(t, filepath.Join(ctx.EnvironmentDirectory(), tt.name), root)
				require.Equal(t, filepath.Join(root, "wd"), workDir)
			} else {
				require.ErrorContains(t, err, "is invalid")
				require.Empty(t, root)
				require.ErrorContains(t, workErr, "is invalid")
				require.Empty(t, workDir)
			}

			// Write untrusted metadata directly, bypassing SetProjectState.
			require.NoError(t, os.MkdirAll(ctx.EnvironmentDirectory(), 0700))
			configPath := filepath.Join(ctx.EnvironmentDirectory(), ConfigFileName)
			raw, err := json.Marshal(configFile{DefaultEnvironment: tt.name})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(configPath, raw, 0600))

			name, err := ctx.GetDefaultEnvironmentName()
			if tt.valid || tt.name == "" {
				require.NoError(t, err)
				require.Equal(t, tt.name, name)
				require.NoError(t, ctx.SetProjectState(ProjectState{DefaultEnvironment: tt.name}))
			} else {
				require.ErrorContains(t, err, "is invalid")
				require.Empty(t, name)
				require.ErrorContains(t, ctx.SetProjectState(ProjectState{DefaultEnvironment: tt.name}), "is invalid")
				actual, err := os.ReadFile(configPath)
				require.NoError(t, err)
				require.Equal(t, raw, actual)
			}
		})
	}
}
