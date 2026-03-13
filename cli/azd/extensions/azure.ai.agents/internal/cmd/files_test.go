// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaiagent/internal/pkg/agents/agent_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilesCommand_HasSubcommands(t *testing.T) {
	cmd := newFilesCommand()

	subcommands := cmd.Commands()
	names := make([]string, len(subcommands))
	for i, c := range subcommands {
		names[i] = c.Name()
	}

	assert.Contains(t, names, "upload")
	assert.Contains(t, names, "download")
	assert.Contains(t, names, "list")
	assert.Contains(t, names, "remove")
}

func TestFilesUploadCommand_RequiredFlags(t *testing.T) {
	cmd := newFilesUploadCommand()

	// No flags and no args should fail
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestFilesUploadCommand_MissingName(t *testing.T) {
	cmd := newFilesUploadCommand()

	cmd.SetArgs([]string{"/remote/path", "--path", "local.txt", "--version", "1", "--session", "abc"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "name")
}

func TestFilesUploadCommand_MissingVersion(t *testing.T) {
	cmd := newFilesUploadCommand()

	cmd.SetArgs([]string{"/remote/path", "--path", "local.txt", "--name", "agent", "--session", "abc"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "version")
}

func TestFilesUploadCommand_MissingSession(t *testing.T) {
	cmd := newFilesUploadCommand()

	cmd.SetArgs([]string{"/remote/path", "--path", "local.txt", "--name", "agent", "--version", "1"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "session")
}

func TestFilesUploadCommand_MissingPath(t *testing.T) {
	cmd := newFilesUploadCommand()

	cmd.SetArgs([]string{"/remote/path", "--name", "agent", "--version", "1", "--session", "abc"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestFilesDownloadCommand_RequiredFlags(t *testing.T) {
	cmd := newFilesDownloadCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestFilesDownloadCommand_DefaultOutputPath(t *testing.T) {
	cmd := newFilesDownloadCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "", output, "output should default to empty (uses remote filename)")
}

func TestFilesListCommand_RequiredFlags(t *testing.T) {
	cmd := newFilesListCommand()

	// No flags should fail due to missing required flags
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestFilesListCommand_DefaultOutputFormat(t *testing.T) {
	cmd := newFilesListCommand()

	output, _ := cmd.Flags().GetString("output")
	assert.Equal(t, "json", output)
}

func TestFilesListCommand_OptionalRemotePath(t *testing.T) {
	cmd := newFilesListCommand()

	// Verify the command accepts 0 or 1 args
	assert.NotNil(t, cmd.Args)
}

func TestFilesRemoveCommand_RequiredFlags(t *testing.T) {
	cmd := newFilesRemoveCommand()

	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestFilesRemoveCommand_RecursiveDefault(t *testing.T) {
	cmd := newFilesRemoveCommand()

	recursive, _ := cmd.Flags().GetBool("recursive")
	assert.False(t, recursive, "recursive should default to false")
}

func TestPrintFileListJSON(t *testing.T) {
	fileList := &agent_api.SessionFileList{
		Files: []agent_api.SessionFileInfo{
			{
				Name:         "test.txt",
				Path:         "/data/test.txt",
				IsDirectory:  false,
				Size:         1024,
				LastModified: "2025-01-01T00:00:00Z",
			},
			{
				Name:        "subdir",
				Path:        "/data/subdir",
				IsDirectory: true,
			},
		},
	}

	err := printFileListJSON(fileList)
	require.NoError(t, err)
}

func TestPrintFileListTable(t *testing.T) {
	fileList := &agent_api.SessionFileList{
		Files: []agent_api.SessionFileInfo{
			{
				Name:         "test.txt",
				Path:         "/data/test.txt",
				IsDirectory:  false,
				Size:         1024,
				LastModified: "2025-01-01T00:00:00Z",
			},
			{
				Name:        "subdir",
				Path:        "/data/subdir",
				IsDirectory: true,
			},
		},
	}

	err := printFileListTable(fileList)
	require.NoError(t, err)
}

func TestPrintFileListJSON_Empty(t *testing.T) {
	fileList := &agent_api.SessionFileList{
		Files: []agent_api.SessionFileInfo{},
	}

	err := printFileListJSON(fileList)
	require.NoError(t, err)
}

func TestPrintFileListTable_Empty(t *testing.T) {
	fileList := &agent_api.SessionFileList{
		Files: []agent_api.SessionFileInfo{},
	}

	err := printFileListTable(fileList)
	require.NoError(t, err)
}
