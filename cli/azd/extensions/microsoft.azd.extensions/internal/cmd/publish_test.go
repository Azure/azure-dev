// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/extensions"
	"github.com/stretchr/testify/require"
)

func TestSaveRegistryDoesNotHTMLEscape(t *testing.T) {
	registry := &extensions.Registry{
		SchemaVersion: "1.0",
		Extensions: []*extensions.ExtensionMetadata{
			{
				Id:          "microsoft.azd.demo",
				Description: "Run azd demo <command> & inspect results > output",
			},
		},
	}

	path := filepath.Join(t.TempDir(), "registry.json")
	require.NoError(t, saveRegistry(path, registry))

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	content := string(data)
	require.Contains(t, content, "azd demo <command> & inspect results > output")
	require.NotContains(t, content, "\\u003c")
	require.NotContains(t, content, "\\u003e")
	require.NotContains(t, content, "\\u0026")
	require.True(t, strings.HasSuffix(content, "\n"))
}
