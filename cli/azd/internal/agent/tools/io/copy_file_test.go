// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyFileTool_SecurityBoundaryValidation(t *testing.T) {
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
			name:          "windows system file source",
			sourceFile:    outside,
			destFile:      "safe_dest.txt",
			expectError:   true,
			errorContains: "Access denied",
		},
		{
			name:          "windows system file destination",
			setupFile:     "safe_source.txt",
			sourceFile:    "safe_source.txt",
			destFile:      outside,
			expectError:   true,
			errorContains: "Access denied",
		},
		{
			name:        "valid copy within security root",
			setupFile:   "source.txt",
			sourceFile:  "source.txt",
			destFile:    "dest.txt",
			expectError: false,
		},
		{
			name:        "valid copy to subdirectory within security root",
			setupFile:   "source.txt",
			sourceFile:  "source.txt",
			destFile:    "subdir/dest.txt",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := CopyFileTool{securityManager: sm}

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

			request := CopyFileRequest{
				Source:      tt.sourceFile,
				Destination: tt.destFile,
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
				var response CopyFileResponse
				err = json.Unmarshal([]byte(result), &response)
				require.NoError(t, err)
				assert.True(t, response.Success)

				// For successful operations, the file should be copied
				// (Note: We won't verify the file exists if parent directories don't exist)
			}
		})
	}
}
