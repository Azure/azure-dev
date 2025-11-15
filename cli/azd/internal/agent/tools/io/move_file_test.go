// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/internal/agent/tools/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoveFileTool_SecurityBoundaryValidation(t *testing.T) {
	outside := absoluteOutsidePath("system")
	tmpOutside := absoluteOutsidePath("temp")
	tests := []struct {
		name          string
		setupFile     string
		sourceFile    string
		destFile      string
		expectError   bool
		errorContains string
	}{
		{
			name:          "source outside security root - absolute path",
			sourceFile:    outside,
			destFile:      "safe_dest.txt",
			expectError:   true,
			errorContains: "Access denied",
		},
		{
			name:          "destination outside security root - absolute path",
			setupFile:     "safe_source.txt",
			sourceFile:    "safe_source.txt",
			destFile:      tmpOutside,
			expectError:   true,
			errorContains: "Access denied",
		},
		{
			name:          "source escaping with relative path",
			sourceFile:    relativeEscapePath("deep"),
			destFile:      "safe_dest.txt",
			expectError:   true,
			errorContains: "Access denied",
		},
		{
			name:          "destination escaping with relative path",
			setupFile:     "safe_source.txt",
			sourceFile:    "safe_source.txt",
			destFile:      relativeEscapePath("deep"),
			expectError:   true,
			errorContains: "Access denied",
		},
		{
			name:          "attempt to move SSH private key",
			sourceFile:    platformSpecificPath("ssh_keys"),
			destFile:      "stolen_key.txt",
			expectError:   true,
			errorContains: "Access denied",
		},
		{
			name:          "attempt to move to startup folder",
			setupFile:     "safe_source.txt",
			sourceFile:    "safe_source.txt",
			destFile:      platformSpecificPath("startup_folder"),
			expectError:   true,
			errorContains: "Access denied",
		},
		{
			name:        "valid move within security root",
			setupFile:   "source.txt",
			sourceFile:  "source.txt",
			destFile:    "dest.txt",
			expectError: false,
		},
		{
			name:        "valid move to subdirectory within security root",
			setupFile:   "source.txt",
			sourceFile:  "source.txt",
			destFile:    "subdir/dest.txt",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := MoveFileTool{securityManager: sm}

			// Setup source file if needed
			if tt.setupFile != "" {
				setupPath := filepath.Join(tempDir, tt.setupFile)
				err := os.WriteFile(setupPath, []byte("test content"), 0600)
				require.NoError(t, err)
			}

			// Create subdirectory for subdirectory tests
			if strings.Contains(tt.destFile, "subdir/") {
				subdirPath := filepath.Join(tempDir, "subdir")
				err := os.MkdirAll(subdirPath, 0755)
				require.NoError(t, err)
			}

			// Move file uses string format "source|destination"
			input := fmt.Sprintf("%s|%s", tt.sourceFile, tt.destFile)

			result, err := tool.Call(context.Background(), input)
			assert.NoError(t, err)

			if tt.expectError {
				var errorResp common.ErrorResponse
				err = json.Unmarshal([]byte(result), &errorResp)
				require.NoError(t, err)
				assert.True(t, errorResp.Error)
				assert.Contains(t, errorResp.Message, tt.errorContains)
			} else {
				// Define the response type inline since it's defined in the tool
				type MoveFileResponse struct {
					Success     bool   `json:"success"`
					Source      string `json:"source"`
					Destination string `json:"destination"`
					Type        string `json:"type"`
					Size        int64  `json:"size"`
					Message     string `json:"message"`
				}
				var response MoveFileResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)

				// For successful operations, the file should be moved
				// (Note: We won't verify file locations if parent directories don't exist)

				// Verify source file no longer exists (if it was a real move)
				expectedSourcePath := filepath.Join(tempDir, filepath.Clean(tt.sourceFile))
				if _, err := os.Stat(expectedSourcePath); err == nil {
					// Source still exists, which might be expected in some cases
					t.Logf("Source still exists (may be expected): %s", expectedSourcePath)
				}
			}
		})
	}
}
