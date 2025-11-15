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

func TestDeleteDirectoryTool_SecurityBoundaryValidation(t *testing.T) {
	tests := []struct {
		name          string
		setupDir      string
		deleteDir     string
		expectError   bool
		errorContains string
	}{
		{
			name:          "absolute path outside security root",
			deleteDir:     absoluteOutsidePath("temp"),
			expectError:   true,
			errorContains: "Access denied: directory deletion operation not permitted outside the allowed directory",
		},
		{
			name:          "relative path escaping with ..",
			deleteDir:     relativeEscapePath("deep"),
			expectError:   true,
			errorContains: "Access denied: directory deletion operation not permitted outside the allowed directory",
		},
		{
			name:          "directory outside security root",
			deleteDir:     absoluteOutsidePath("system"),
			expectError:   true,
			errorContains: "Access denied: directory deletion operation not permitted outside the allowed directory",
		},
		{
			name:          "attempt to delete root directory",
			deleteDir:     absoluteOutsidePath("root"),
			expectError:   true,
			errorContains: "Access denied: directory deletion operation not permitted outside the allowed directory",
		},
		{
			name:        "valid directory within security root",
			setupDir:    "test_dir",
			deleteDir:   "test_dir",
			expectError: false,
		},
		{
			name:        "valid nested directory within security root",
			setupDir:    "parent/child",
			deleteDir:   "parent/child",
			expectError: false,
		},
		{
			name:        "current directory reference within security root",
			setupDir:    "test_dir",
			deleteDir:   "./test_dir",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := DeleteDirectoryTool{securityManager: sm}

			// Setup test directory if needed
			if tt.setupDir != "" {
				dirPath := filepath.Join(tempDir, tt.setupDir)
				err := os.MkdirAll(dirPath, 0755)
				require.NoError(t, err)
			}

			result, err := tool.Call(context.Background(), tt.deleteDir)
			assert.NoError(t, err)

			if tt.expectError {
				var errorResp common.ErrorResponse
				err = json.Unmarshal([]byte(result), &errorResp)
				require.NoError(t, err)
				assert.True(t, errorResp.Error)
				assert.Contains(t, errorResp.Message, tt.errorContains)
			} else {
				// Define the response type inline since it's defined in the tool
				type DeleteDirectoryResponse struct {
					Success      bool   `json:"success"`
					Path         string `json:"path"`
					ItemsDeleted int    `json:"itemsDeleted"`
					Message      string `json:"message"`
				}
				var response DeleteDirectoryResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)

				// Verify directory was deleted
				expectedPath := filepath.Join(tempDir, filepath.Clean(tt.deleteDir))
				_, err = os.Stat(expectedPath)
				assert.True(t, os.IsNotExist(err))
			}
		})
	}
}
