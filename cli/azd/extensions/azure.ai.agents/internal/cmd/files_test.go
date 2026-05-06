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
	cmd := newFilesCommand(nil)

	subcommands := cmd.Commands()
	names := make([]string, len(subcommands))
	for i, c := range subcommands {
		names[i] = c.Name()
	}

	assert.Contains(t, names, "upload")
	assert.Contains(t, names, "download")
	assert.Contains(t, names, "list")
	assert.Contains(t, names, "delete")
}

func TestFilesUploadCommand_MissingFile(t *testing.T) {
	cmd := newFilesUploadCommand(nil)

	// Missing required --file flag
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file")
}

func TestFilesUploadCommand_HasFlags(t *testing.T) {
	cmd := newFilesUploadCommand(nil)

	for _, name := range []string{"file", "target-path", "agent-name", "session-id"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "expected flag %q", name)
		assert.Equal(t, "", f.DefValue)
	}
}

func TestFilesDownloadCommand_MissingFile(t *testing.T) {
	cmd := newFilesDownloadCommand(nil)

	// Missing required --file flag
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file")
}

func TestFilesDownloadCommand_HasFlags(t *testing.T) {
	cmd := newFilesDownloadCommand(nil)

	for _, name := range []string{"file", "target-path", "agent-name", "session-id"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "expected flag %q", name)
		assert.Equal(t, "", f.DefValue)
	}
}

func TestFilesListCommand_DefaultOutputFormat(t *testing.T) {
	cmd := newFilesListCommand(nil)
	assertOutputFlagOptions(t, cmd, "json", []string{"json", "table"})
}

func TestFilesListCommand_OptionalRemotePath(t *testing.T) {
	cmd := newFilesListCommand(nil)

	// Verify the command accepts 0 or 1 args
	assert.NotNil(t, cmd.Args)
}

func TestFilesDeleteCommand_MissingFile(t *testing.T) {
	cmd := newFilesRemoveCommand(nil)

	// Missing required --file flag
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file")
}

func TestFilesDeleteCommand_HasFlags(t *testing.T) {
	cmd := newFilesRemoveCommand(nil)

	for _, name := range []string{"file", "recursive", "agent-name", "session-id"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "expected flag %q", name)
	}

	recursive, _ := cmd.Flags().GetBool("recursive")
	assert.False(t, recursive, "recursive should default to false")
}

func TestFilesMkdirCommand_MissingDir(t *testing.T) {
	cmd := newFilesMkdirCommand(nil)

	// Missing required --dir flag
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "dir")
}

func TestFilesMkdirCommand_HasFlags(t *testing.T) {
	cmd := newFilesMkdirCommand(nil)

	for _, name := range []string{"dir", "agent-name", "session-id"} {
		f := cmd.Flags().Lookup(name)
		require.NotNil(t, f, "expected flag %q", name)
		assert.Equal(t, "", f.DefValue)
	}
}

func TestPrintFileListJSON(t *testing.T) {
	modified := agent_api.FlexibleTimestamp("2025-01-01T00:00:00Z")
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
	modified := agent_api.FlexibleTimestamp("2025-01-01T00:00:00Z")
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
