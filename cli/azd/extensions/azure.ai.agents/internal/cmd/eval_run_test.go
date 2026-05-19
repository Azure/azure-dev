// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"azureaiagent/internal/pkg/agents/opteval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// newEvalRunCommand — command shape
// ---------------------------------------------------------------------------

func TestNewEvalRunCommand_Flags(t *testing.T) {
	t.Parallel()
	cmd := newEvalRunCommand(nil)

	f := cmd.Flags().Lookup("config")
	require.NotNil(t, f)
	assert.Equal(t, defaultEvalConfigName, f.DefValue)
}

func TestNewEvalRunCommand_NoArgs(t *testing.T) {
	t.Parallel()
	cmd := newEvalRunCommand(nil)
	assert.NoError(t, cmd.Args(cmd, nil))
	assert.Error(t, cmd.Args(cmd, []string{"extra"}))
}

func TestNewEvalRunCommand_UseString(t *testing.T) {
	t.Parallel()
	cmd := newEvalRunCommand(nil)
	assert.Equal(t, "run", cmd.Use)
}

// ---------------------------------------------------------------------------
// loadEvalDatasetFile
// ---------------------------------------------------------------------------

func TestLoadEvalDatasetFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "data.jsonl")
	content := "{\"query\":\"hello\",\"id\":\"1\"}\n{\"query\":\"world\",\"id\":\"2\"}\n"
	require.NoError(t, os.WriteFile(f, []byte(content), 0600))

	items, err := loadEvalDatasetFile(f)
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "hello", items[0]["query"])
	assert.Equal(t, "2", items[1]["id"])
}

func TestLoadEvalDatasetFile_Empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	f := filepath.Join(dir, "empty.jsonl")
	require.NoError(t, os.WriteFile(f, []byte(""), 0600))

	_, err := loadEvalDatasetFile(f)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "contains no items")
}

func TestLoadEvalDatasetFile_NotFound(t *testing.T) {
	t.Parallel()
	_, err := loadEvalDatasetFile("/nonexistent/data.jsonl")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// parseProjectEndpoint
// ---------------------------------------------------------------------------

func TestParseProjectEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		endpoint        string
		expectedAccount string
		expectedProject string
	}{
		{
			"standard endpoint",
			"https://foundryljm7.services.ai.azure.com/api/projects/projectljm7",
			"foundryljm7",
			"projectljm7",
		},
		{
			"endpoint with trailing slash",
			"https://myaccount.services.ai.azure.com/api/projects/myproject/",
			"myaccount",
			"myproject",
		},
		{
			"empty string",
			"",
			"",
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account, project := parseProjectEndpoint(tt.endpoint)
			assert.Equal(t, tt.expectedAccount, account)
			assert.Equal(t, tt.expectedProject, project)
		})
	}
}

// ---------------------------------------------------------------------------
// buildDatasetFileID
// ---------------------------------------------------------------------------

func TestBuildDatasetFileID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		endpoint string
		ref      *opteval.DatasetRef
		expected string
	}{
		{
			"with version",
			"https://foundryljm7.services.ai.azure.com/api/projects/projectljm7",
			&opteval.DatasetRef{Name: "bugbash-mt-sim-scenarios", Version: "1"},
			"azureai://accounts/foundryljm7/projects/projectljm7/data/bugbash-mt-sim-scenarios/versions/1",
		},
		{
			"default version",
			"https://myaccount.services.ai.azure.com/api/projects/myproject",
			&opteval.DatasetRef{Name: "my-dataset"},
			"azureai://accounts/myaccount/projects/myproject/data/my-dataset/versions/1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildDatasetFileID(tt.endpoint, tt.ref)
			assert.Equal(t, tt.expected, result)
		})
	}
}
