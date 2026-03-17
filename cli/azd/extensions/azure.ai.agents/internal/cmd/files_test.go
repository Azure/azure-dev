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

func TestFilesUploadCommand_MissingArgs(t *testing.T) {
	cmd := newFilesUploadCommand()

	// No args should fail (requires remote-path positional arg)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
}

func TestFilesUploadCommand_MissingPath(t *testing.T) {
	cmd := newFilesUploadCommand()

	// Missing required --path flag
	cmd.SetArgs([]string{"/remote/path"})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestFilesUploadCommand_HasServiceFlag(t *testing.T) {
	cmd := newFilesUploadCommand()

	f := cmd.Flags().Lookup("service")
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestFilesUploadCommand_HasSessionFlag(t *testing.T) {
	cmd := newFilesUploadCommand()

	f := cmd.Flags().Lookup("session")
	require.NotNil(t, f)
	assert.Equal(t, "", f.DefValue)
}

func TestFilesDownloadCommand_MissingArgs(t *testing.T) {
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

func TestFilesRemoveCommand_MissingArgs(t *testing.T) {
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
	modified := "2025-01-01T00:00:00Z"
	fileList := &agent_api.SessionFileList{
		Path: "/data",
		Entries: []agent_api.SessionFileInfo{
			{
				Name:         "test.txt",
				Path:         "/data/test.txt",
				IsDirectory:  false,
				Size:         1024,
				LastModified: &modified,
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
	modified := "2025-01-01T00:00:00Z"
	fileList := &agent_api.SessionFileList{
		Path: "/data",
		Entries: []agent_api.SessionFileInfo{
			{
				Name:         "test.txt",
				Path:         "/data/test.txt",
				IsDirectory:  false,
				Size:         1024,
				LastModified: &modified,
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
		Path:    "/",
		Entries: []agent_api.SessionFileInfo{},
	}

	err := printFileListJSON(fileList)
	require.NoError(t, err)
}

func TestPrintFileListTable_Empty(t *testing.T) {
	fileList := &agent_api.SessionFileList{
		Path:    "/",
		Entries: []agent_api.SessionFileInfo{},
	}

	err := printFileListTable(fileList)
	require.NoError(t, err)
}
