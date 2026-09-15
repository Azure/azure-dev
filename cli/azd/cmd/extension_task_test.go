// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/stretchr/testify/require"
)

func TestExtensionTaskMessage(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"Installing "+output.WithHighLightFormat("azure.ai.rle"),
		extensionTaskMessage("Installing", "azure.ai.rle"),
	)
	require.Equal(
		t,
		"Uninstalling "+output.WithHighLightFormat("azure.ai.rle")+
			output.WithGrayFormat(" (1.2.3)"),
		extensionTaskMessageWithVersion("Uninstalling", "azure.ai.rle", "1.2.3"),
	)
}
