// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package io

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileSearchTool_SecurityBoundaryValidation(t *testing.T) {
	tests := []struct {
		name        string
		setupFiles  []string
		pattern     string
		expectError bool
	}{
		{
			name:        "valid pattern within security root",
			setupFiles:  []string{"test1.txt", "test2.log"},
			pattern:     "*.txt",
			expectError: false,
		},
		{
			name:        "recursive pattern within security root",
			setupFiles:  []string{"subdir/test1.txt"},
			pattern:     "**/test*.txt",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm, tempDir := createTestSecurityManager(t)
			tool := FileSearchTool{securityManager: sm}

			// Setup files
			for _, file := range tt.setupFiles {
				filePath := filepath.Join(tempDir, file)
				err := os.MkdirAll(filepath.Dir(filePath), 0755)
				require.NoError(t, err)
				err = os.WriteFile(filePath, []byte("test content"), 0600)
				require.NoError(t, err)
			}

			request := FileSearchRequest{
				Pattern: tt.pattern,
			}
			input := mustMarshalJSON(request)

			result, err := tool.Call(context.Background(), input)
			assert.NoError(t, err)

			type FileSearchResponse struct {
				Success    bool     `json:"success"`
				Pattern    string   `json:"pattern"`
				TotalFound int      `json:"totalFound"`
				Returned   int      `json:"returned"`
				MaxResults int      `json:"maxResults"`
				Files      []string `json:"files"`
				Message    string   `json:"message"`
			}
			var response FileSearchResponse
			err = json.Unmarshal([]byte(result), &response)
			require.NoError(t, err)
			assert.True(t, response.Success)
		})
	}
}
