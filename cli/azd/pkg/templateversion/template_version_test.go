// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package templateversion

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetShortCommitHash(t *testing.T) {
	console := mocks.NewMockConsole(t)
	runner := mocks.NewMockCommandRunner(t)
	manager := NewManager(console, runner)

	// Mock the git command
	runner.EXPECT().Run(mock.Anything, mock.MatchedBy(func(args exec.RunArgs) bool {
		return args.Cmd == "git" && len(args.Args) == 3 && args.Args[0] == "rev-parse"
	})).Return(&exec.RunResult{
		ExitCode: 0,
		Stdout:   "abc1234\n",
	}, nil)

	// Call the function
	hash, err := manager.GetShortCommitHash(context.Background(), "/test/path")

	// Assert
	assert.NoError(t, err)
	assert.Equal(t, "abc1234", hash)
}

func TestCreateVersionFile(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "templateversion_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	console := mocks.NewMockConsole(t)
	runner := mocks.NewMockCommandRunner(t)
	manager := NewManager(console, runner)

	// Mock the console
	console.EXPECT().Message(mock.Anything, mock.Anything).Return()

	// Mock the git command
	runner.EXPECT().Run(mock.Anything, mock.MatchedBy(func(args exec.RunArgs) bool {
		return args.Cmd == "git" && len(args.Args) == 3 && args.Args[0] == "rev-parse"
	})).Return(&exec.RunResult{
		ExitCode: 0,
		Stdout:   "abc1234\n",
	}, nil)

	// Call the function
	version, err := manager.CreateVersionFile(context.Background(), tempDir)
	require.NoError(t, err)

	// Assert that the file exists
	filePath := filepath.Join(tempDir, VersionFileName)
	assert.FileExists(t, filePath)

	// Read the file content
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)

	// Assert the content
	assert.Equal(t, version, string(content))

	// Check file permissions
	info, err := os.Stat(filePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(ReadOnlyFilePerms), info.Mode().Perm())
}

func TestReadVersionFile(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "templateversion_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a version file
	filePath := filepath.Join(tempDir, VersionFileName)
	versionContent := "2025-07-18-abc1234"
	err = os.WriteFile(filePath, []byte(versionContent), 0644)
	require.NoError(t, err)

	console := mocks.NewMockConsole(t)
	runner := mocks.NewMockCommandRunner(t)
	manager := NewManager(console, runner)

	// Call the function
	version, err := manager.ReadVersionFile(tempDir)
	require.NoError(t, err)

	// Assert
	assert.Equal(t, versionContent, version)
}

func TestReadVersionFileNotExists(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "templateversion_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	console := mocks.NewMockConsole(t)
	runner := mocks.NewMockCommandRunner(t)
	manager := NewManager(console, runner)

	// Call the function
	version, err := manager.ReadVersionFile(tempDir)
	require.NoError(t, err)

	// Assert
	assert.Equal(t, "", version)
}

func TestParseVersionString(t *testing.T) {
	tests := []struct {
		name         string
		version      string
		expectError  bool
		expectedErr  string
		expectedDate string
		expectedHash string
	}{
		{
			name:         "Valid version",
			version:      "2025-07-18-abc1234",
			expectError:  false,
			expectedDate: "2025-07-18",
			expectedHash: "abc1234",
		},
		{
			name:        "Empty version",
			version:     "",
			expectError: true,
			expectedErr: "empty version string",
		},
		{
			name:        "Too few parts",
			version:     "2023-04-05",
			expectError: true,
			expectedErr: "invalid version string format",
		},
		{
			name:        "Invalid date format",
			version:     "20230-04-05-abcdef1",
			expectError: true,
			expectedErr: "invalid date format",
		},
		{
			name:        "Invalid format",
			version:     "invalid-version",
			expectError: true,
			expectedErr: "invalid version string format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseVersionString(tt.version)
			if tt.expectError {
				assert.Error(t, err)
				if tt.expectedErr != "" {
					assert.Contains(t, err.Error(), tt.expectedErr)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedDate, info.Date)
				assert.Equal(t, tt.expectedHash, info.CommitHash)
				assert.Equal(t, tt.version, info.FullVersion)
			}
		})
	}
}

func TestEnsureTemplateVersion_Exists(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "templateversion_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a version file
	filePath := filepath.Join(tempDir, VersionFileName)
	versionContent := "2025-07-18-abc1234"
	err = os.WriteFile(filePath, []byte(versionContent), 0644)
	require.NoError(t, err)

	console := mocks.NewMockConsole(t)
	runner := mocks.NewMockCommandRunner(t)
	manager := NewManager(console, runner)

	// Call the function
	version, err := manager.EnsureTemplateVersion(context.Background(), tempDir)
	require.NoError(t, err)

	// Assert
	assert.Equal(t, versionContent, version)
}

func TestEnsureTemplateVersion_NotExists(t *testing.T) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "templateversion_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	console := mocks.NewMockConsole(t)
	runner := mocks.NewMockCommandRunner(t)
	manager := NewManager(console, runner)

	// Mock the console
	console.EXPECT().Message(mock.Anything, mock.Anything).Return()

	// Mock the git command
	runner.EXPECT().Run(mock.Anything, mock.MatchedBy(func(args exec.RunArgs) bool {
		return args.Cmd == "git" && len(args.Args) == 3 && args.Args[0] == "rev-parse"
	})).Return(&exec.RunResult{
		ExitCode: 0,
		Stdout:   "abc1234\n",
	}, nil)

	// Call the function
	version, err := manager.EnsureTemplateVersion(context.Background(), tempDir)
	require.NoError(t, err)

	// Assert that the file exists
	filePath := filepath.Join(tempDir, VersionFileName)
	assert.FileExists(t, filePath)

	// Read the file content
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)

	// Assert the content
	assert.Equal(t, version, string(content))
}
