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

func TestDeleteFileTool_SecurityBoundaryValidation(t *testing.T) {
	outside := absoluteOutsidePath("system")
	sshOutside := platformSpecificPath("ssh_keys")
	tests := []struct {
		name          string
		setupFile     string
		deleteFile    string
		expectError   bool
		errorContains string
	}{
		{
			name:          "delete file outside security root - absolute path",
			deleteFile:    outside,
			expectError:   true,
			errorContains: "Access denied: file deletion operation not permitted outside the allowed directory",
		},
		{
			name:          "delete file escaping with relative path",
			deleteFile:    relativeEscapePath("deep"),
			expectError:   true,
			errorContains: "Access denied: file deletion operation not permitted outside the allowed directory",
		},
		{
			name:          "delete windows system file",
			deleteFile:    outside,
			expectError:   true,
			errorContains: "Access denied: file deletion operation not permitted outside the allowed directory",
		},
		{
			name:          "delete SSH private key",
			deleteFile:    sshOutside,
			expectError:   true,
			errorContains: "Access denied: file deletion operation not permitted outside the allowed directory",
		},
		{
			name:          "delete shell configuration",
			deleteFile:    platformSpecificPath("shell_config"),
			expectError:   true,
			errorContains: "Access denied: file deletion operation not permitted outside the allowed directory",
		},
		{
			name:          "delete hosts file",
			deleteFile:    platformSpecificPath("hosts"),
			expectError:   true,
			errorContains: "Access denied: file deletion operation not permitted outside the allowed directory",
		},
		{
			name:        "valid delete within security root",
			setupFile:   "test_file.txt",
			deleteFile:  "test_file.txt",
			expectError: false,
		},
		{
			name:        "valid delete subdirectory file within security root",
			setupFile:   "subdir/test_file.txt",
			deleteFile:  "subdir/test_file.txt",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := DeleteFileTool{securityManager: sm}

			// Setup file to delete if needed
			if tt.setupFile != "" {
				setupPath := filepath.Join(tempDir, tt.setupFile)
				err := os.MkdirAll(filepath.Dir(setupPath), 0755)
				require.NoError(t, err)
				err = os.WriteFile(setupPath, []byte("test content"), 0600)
				require.NoError(t, err)
			}

			result, err := tool.Call(context.Background(), tt.deleteFile)
			assert.NoError(t, err)

			if tt.expectError {
				var errorResp common.ErrorResponse
				err = json.Unmarshal([]byte(result), &errorResp)
				require.NoError(t, err)
				assert.True(t, errorResp.Error)
				assert.Contains(t, errorResp.Message, tt.errorContains)
			} else {
				// Define the response type inline since it's defined in the tool
				type DeleteFileResponse struct {
					Success     bool   `json:"success"`
					FilePath    string `json:"filePath"`
					SizeDeleted int64  `json:"sizeDeleted"`
					Message     string `json:"message"`
				}
				var response DeleteFileResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)

				// Verify file was deleted
				expectedPath := filepath.Join(tempDir, filepath.Clean(tt.deleteFile))
				_, err = os.Stat(expectedPath)
				assert.True(t, os.IsNotExist(err))
			}
		})
	}
}
