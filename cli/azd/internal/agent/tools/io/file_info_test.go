// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileInfoTool_SecurityBoundaryValidation(t *testing.T) {
	tests := []struct {
		name          string
		setupFile     string
		filePath      string
		expectError   bool
		errorContains string
	}{
		{
			name:          "absolute path outside security root",
			filePath:      absoluteOutsidePath("system"),
			expectError:   true,
			errorContains: "Access denied: file info operation not permitted outside the allowed directory",
		},
		{
			name:          "relative path escaping with ..",
			filePath:      relativeEscapePath("with_file"),
			expectError:   true,
			errorContains: "Access denied: file info operation not permitted outside the allowed directory",
		},
		{
			name:          "windows system file",
			filePath:      absoluteOutsidePath("system"),
			expectError:   true,
			errorContains: "Access denied: file info operation not permitted outside the allowed directory",
		},
		{
			name:        "valid file within security root",
			setupFile:   "test_file.txt",
			filePath:    "test_file.txt",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := FileInfoTool{securityManager: sm}

			// Setup file if needed
			if tt.setupFile != "" {
				setupPath := filepath.Join(tempDir, tt.setupFile)
				err := os.WriteFile(setupPath, []byte("test content"), 0600)
				require.NoError(t, err)
			}

			result, err := tool.Call(context.Background(), tt.filePath)
			assert.NoError(t, err)

			if tt.expectError {
				var errorResp common.ErrorResponse
				err = json.Unmarshal([]byte(result), &errorResp)
				require.NoError(t, err)
				assert.True(t, errorResp.Error)
				assert.Contains(t, errorResp.Message, tt.errorContains)
			} else {
				type FileInfoResponse struct {
					Success      bool      `json:"success"`
					Path         string    `json:"path"`
					Name         string    `json:"name"`
					Type         string    `json:"type"`
					IsDirectory  bool      `json:"isDirectory"`
					Size         int64     `json:"size"`
					ModifiedTime time.Time `json:"modifiedTime"`
					Permissions  string    `json:"permissions"`
					Message      string    `json:"message"`
				}
				var response FileInfoResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)
				assert.NotEmpty(t, response.Name)
			}
		})
	}
}
