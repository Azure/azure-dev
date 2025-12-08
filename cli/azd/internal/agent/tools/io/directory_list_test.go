// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDirectoryListTool_SecurityBoundaryValidation(t *testing.T) {
	tests := []struct {
		name          string
		setupDirs     []string
		listPath      string
		expectError   bool
		errorContains string
	}{
		{
			name:          "absolute path outside security root",
			listPath:      absoluteOutsidePath("system"),
			expectError:   true,
			errorContains: "Access denied: directory listing operation not permitted outside the allowed directory",
		},
		{
			name:          "relative path escaping with ..",
			listPath:      relativeEscapePath("deep"),
			expectError:   true,
			errorContains: "Access denied: directory listing operation not permitted outside the allowed directory",
		},
		{
			name:          "windows system directory",
			listPath:      absoluteOutsidePath("system"),
			expectError:   true,
			errorContains: "Access denied: directory listing operation not permitted outside the allowed directory",
		},
		{
			name:          "attempt to list root directory",
			listPath:      platformSpecificPath("system_drive"),
			expectError:   true,
			errorContains: "Access denied: directory listing operation not permitted outside the allowed directory",
		},
		{
			name:        "valid directory within security root",
			setupDirs:   []string{"test_dir"},
			listPath:    "test_dir",
			expectError: false,
		},
		{
			name:        "valid nested directory within security root",
			setupDirs:   []string{"parent/child"},
			listPath:    "parent/child",
			expectError: false,
		},
		{
			name:        "current directory reference",
			setupDirs:   []string{"file1.txt", "dir1"},
			listPath:    ".",
			expectError: false,
		},
		{
			name:        "security root itself",
			setupDirs:   []string{"file1.txt", "dir1"},
			listPath:    "",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := DirectoryListTool{securityManager: sm}

			// Setup test files and directories
			for _, setupItem := range tt.setupDirs {
				itemPath := filepath.Join(tempDir, setupItem)
				if filepath.Ext(setupItem) != "" {
					// Create file
					err := os.WriteFile(itemPath, []byte("test"), 0600)
					require.NoError(t, err)
				} else {
					// Create directory
					err := os.MkdirAll(itemPath, 0755)
					require.NoError(t, err)
				}
			}

			request := DirectoryListRequest{
				Path: tt.listPath,
			}
			input := mustMarshalJSON(request)

			result, err := tool.Call(context.Background(), input)
			assert.NoError(t, err)

			if tt.expectError {
				var errorResp common.ErrorResponse
				err = json.Unmarshal([]byte(result), &errorResp)
				require.NoError(t, err)
				assert.True(t, errorResp.Error)
				assert.Contains(t, errorResp.Message, tt.errorContains)
			} else {
				var response DirectoryListResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)
				// Additional verification could be added here to check the contents
			}
		})
	}
}
