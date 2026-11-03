// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build linux || darwin

package infra

import (
	"os"
	"path/filepath"
	"testing"

	"azure.ai.content-understanding/internal/localio"
	"azure.ai.content-understanding/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratedProjectsAndModelsRejectSharedOutputsBeforeMutation(t *testing.T) {
	directory := testutil.TempDir(t)
	_, checkErr := localio.Check(filepath.Join(directory, "models.json"))
	require.NoError(t, checkErr)
	require.NoError(t, os.Chmod(directory, 0o777)) // #nosec G302 -- Deliberately unsafe test directory.
	_, err := Materialize(t.Context(), directory, choices(), false)
	require.Error(t, err)
	backend := modelFixture()
	_, err = SetupModels(t.Context(), backend, "recommended", filepath.Join(directory, "models.json"), true, true)
	require.Error(t, err)
	assert.Empty(t, backend.calls)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	assert.Empty(t, entries, "preflight must not write project/environment/model data")
}
