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

func TestCreateDirectoryTool_SecurityBoundaryValidation(t *testing.T) {
	tests := []struct {
		name          string
		dirPath       string
		expectError   bool
		errorContains string
	}{
		{
			name:          "absolute path outside security root",
			dirPath:       absoluteOutsidePath("temp_dir"),
			expectError:   true,
			errorContains: "Access denied: directory creation operation not permitted outside the allowed directory",
		},
		{
			name:          "directory escaping with relative path",
			dirPath:       relativeEscapePath("deep"),
			expectError:   true,
			errorContains: "Access denied: directory creation operation not permitted outside the allowed directory",
		},
		{
			name:          "windows system directory",
			dirPath:       absoluteOutsidePath("system"),
			expectError:   true,
			errorContains: "Access denied: directory creation operation not permitted outside the allowed directory",
		},
		{
			name:          "attempt to create in root",
			dirPath:       platformSpecificPath("startup_folder"),
			expectError:   true,
			errorContains: "Access denied: directory creation operation not permitted outside the allowed directory",
		},
		{
			name:        "valid directory within security root",
			dirPath:     "safe_dir",
			expectError: false,
		},
		{
			name:        "valid nested directory within security root",
			dirPath:     "parent/child/grandchild",
			expectError: false,
		},
		{
			name:        "current directory reference within security root",
			dirPath:     "./safe_dir",
			expectError: false,
		},
		{
			name:        "complex valid path within security root",
			dirPath:     "parent/../safe_dir",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := CreateDirectoryTool{securityManager: sm}

			result, err := tool.Call(context.Background(), tt.dirPath)
			assert.NoError(t, err)

			if tt.expectError {
				var errorResp common.ErrorResponse
				err = json.Unmarshal([]byte(result), &errorResp)
				require.NoError(t, err)
				assert.True(t, errorResp.Error)
				assert.Contains(t, errorResp.Message, tt.errorContains)
			} else {
				// Define the response type inline since it's defined in the tool
				type CreateDirectoryResponse struct {
					Success bool   `json:"success"`
					Path    string `json:"path"`
					Message string `json:"message"`
				}
				var response CreateDirectoryResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)

				// Verify directory was created in the correct location
				expectedPath := filepath.Join(tempDir, filepath.Clean(tt.dirPath))
				info, err := os.Stat(expectedPath)
				require.NoError(t, err)
				assert.True(t, info.IsDir())
			}
		})
	}
}
