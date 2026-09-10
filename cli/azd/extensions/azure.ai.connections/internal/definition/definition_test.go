// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package definition

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), DefaultPath)
	want := &Definition{
		Name:        "github",
		Category:    "RemoteTool",
		Target:      "https://api.githubcopilot.com/mcp/",
		AuthType:    "CustomKeys",
		Credentials: map[string]any{"Authorization": "${GITHUB_TOKEN}"},
		Metadata:    map[string]string{"owner": "platform"},
	}

	require.NoError(t, Save(path, want))
	got, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), DefaultPath)
	require.NoError(t, os.WriteFile(path, []byte("name: github\nunknown: value\n"), 0o600))

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}
