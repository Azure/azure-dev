// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package ai

import (
	"context"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/python"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_PythonBridge_RequiredExternalTools(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(context.Background())
	pythonCli := python.NewCli(mockContext.CommandRunner)
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	bridge := NewPythonBridge(azdCtx, pythonCli)
	tools := bridge.RequiredExternalTools(*mockContext.Context)
	require.Len(t, tools, 1)
	require.Same(t, pythonCli, tools[0])
}

func Test_PythonBridge_Initialize_Idempotent(t *testing.T) {
	// Avoid t.Parallel here because the sibling Test_PythonBridge_Init already sets
	// AZD_CONFIG_DIR via os.Setenv — we only test the short-circuit branch.
	mockContext := mocks.NewMockContext(context.Background())
	pythonCli := python.NewCli(mockContext.CommandRunner)
	azdCtx := azdcontext.NewAzdContextWithDirectory(t.TempDir())

	bridge := &pythonBridge{
		azdCtx:      azdCtx,
		pythonCli:   pythonCli,
		initialized: true, // short-circuit path
	}

	require.NoError(t, bridge.Initialize(*mockContext.Context))
}
