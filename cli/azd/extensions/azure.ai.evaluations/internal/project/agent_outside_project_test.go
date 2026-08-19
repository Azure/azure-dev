// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// azd supports a service declared with an absolute `project:`, and the whole
// checkout for that service then sits outside the project directory. Containing
// its instruction pointer to the project refused an ordinary relative pointer
// and failed generate outright, where the service's own directory is the
// boundary that means anything for it.
func TestAnAgentDeclaredOutsideTheProjectStillReadsItsInstructions(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "proj")
	require.NoError(t, os.MkdirAll(project, 0o750))

	elsewhere := filepath.Join(root, "shared", "agent")
	require.NoError(t, os.MkdirAll(elsewhere, 0o750))
	writeOptimizeConfig(t, elsewhere, "instruction_file: instructions.md\n", "answer briefly")

	svc := &azdext.ServiceConfig{Name: "agent", Host: AgentHost, RelativePath: elsewhere}
	proj := &azdext.ProjectConfig{Path: project, Services: map[string]*azdext.ServiceConfig{"agent": svc}}

	instruction, path, err := AgentInstructionsFromProject(proj, "agent")

	require.NoError(t, err)
	assert.Equal(t, "answer briefly", instruction)
	assert.Contains(t, path, "instructions.md")
}

// And the boundary still holds there: a pointer climbing out of the service's
// own directory is refused just as one climbing out of the project is.
func TestAnAgentOutsideTheProjectStillCannotReachFurtherOut(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "secret.md"),
		[]byte("a file the checkout does not contain"), 0o600))

	project := filepath.Join(root, "proj")
	require.NoError(t, os.MkdirAll(project, 0o750))

	elsewhere := filepath.Join(root, "shared", "agent")
	require.NoError(t, os.MkdirAll(elsewhere, 0o750))
	writeOptimizeConfig(t, elsewhere, "instruction_file: ../../../secret.md\n", "")

	svc := &azdext.ServiceConfig{Name: "agent", Host: AgentHost, RelativePath: elsewhere}
	proj := &azdext.ProjectConfig{Path: project, Services: map[string]*azdext.ServiceConfig{"agent": svc}}

	_, _, err := AgentInstructionsFromProject(proj, "agent")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the project")
	assert.NotContains(t, err.Error(), "a file the checkout does not contain")
}
