// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChangeDirectoryTool_SecurityBoundaryValidation(t *testing.T) {
	tests := []struct {
		name          string
		setupDirs     []string
		targetDir     string
		expectError   bool
		errorContains string
	}{
		{
			name:          "absolute path outside security root",
			targetDir:     absoluteOutsidePath("temp"),
			expectError:   true,
			errorContains: "Access denied: directory change operation not permitted outside the allowed directory",
		},
		{
			name:          "relative path escaping with ..",
			targetDir:     relativeEscapePath("simple"),
			expectError:   true,
			errorContains: "Access denied: directory change operation not permitted outside the allowed directory",
		},
		{
			name:        "valid directory within security root",
			setupDirs:   []string{"subdir"},
			targetDir:   "subdir",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := ChangeDirectoryTool{securityManager: sm}

			// Setup directories
			for _, dir := range tt.setupDirs {
				dirPath := filepath.Join(tempDir, dir)
				err := os.MkdirAll(dirPath, 0755)
				require.NoError(t, err)
			}

			result, err := tool.Call(context.Background(), tt.targetDir)
			assert.NoError(t, err)

			if tt.expectError {
				var errorResp common.ErrorResponse
				err = json.Unmarshal([]byte(result), &errorResp)
				require.NoError(t, err)
				assert.True(t, errorResp.Error)
				assert.Contains(t, errorResp.Message, tt.errorContains)
			} else {
				type ChangeDirectoryResponse struct {
					Success bool   `json:"success"`
					OldPath string `json:"oldPath,omitempty"`
					NewPath string `json:"newPath"`
					Message string `json:"message"`
				}
				var response ChangeDirectoryResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)
				assert.NotEmpty(t, response.NewPath)
			}
		})
	}
}
