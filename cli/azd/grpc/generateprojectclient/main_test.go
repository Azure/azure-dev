// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectDescriptorSnapshotIsCurrent(t *testing.T) {
	for _, extension := range []string{"azure.ai.evaluations", "azure.ai.connections"} {
		t.Run(extension, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "descriptors_generated.go")
			require.NoError(t, generate(output, extension == "azure.ai.connections"))
			actual, err := os.ReadFile(output)
			require.NoError(t, err)
			expected, err := os.ReadFile(filepath.Join("..", "..", "extensions", extension,
				"internal", "hostproject", "descriptors_generated.go"))
			require.NoError(t, err)
			require.Equal(t, string(expected), string(actual), "run make proto to refresh the canonical descriptor snapshot")
		})
	}
}
