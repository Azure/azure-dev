// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The optimize metadata's instruction_file pointer is only as trustworthy as
// the checkout it was read from. Left unchecked, cloning a repository and
// running generate would read a named local file and send it on as agent
// instructions.
func TestInstructionPointerCannotLeaveTheProject(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work", "proj")

	inside := []string{
		filepath.Join(root, "instructions.md"),
		filepath.Join(root, "src", "agent", ".agent_configs", "baseline", "i.md"),
		filepath.Join(root, "a", "..", "b", "i.md"),
		root,
	}
	for _, p := range inside {
		assert.Truef(t, withinDir(root, p), "%q is inside the project", p)
	}

	outside := []string{
		filepath.Join(root, "..", "other", "secrets.txt"),
		filepath.Join(root, "..", "..", "etc", "passwd"),
		filepath.Join(string(filepath.Separator), "etc", "passwd"),
		filepath.Join(root+"-sibling", "i.md"), // prefix match, different directory
	}
	for _, p := range outside {
		assert.Falsef(t, withinDir(root, p), "%q is outside the project", p)
	}
}

// The guard is worth nothing if the command does not consult it, so this asks
// the command rather than the helper: both escapes have to come back as the
// refusal, not as the contents of the file they named.
func TestGenerateRefusesInstructionsFromOutsideTheProject(t *testing.T) {
	t.Run("named with ..", func(t *testing.T) {
		root := t.TempDir()
		secret := filepath.Join(root, "outside-secret.md")
		require.NoError(t, os.WriteFile(secret, []byte("a file the project does not contain"), 0o600))

		project := filepath.Join(root, "proj")
		serviceDir := filepath.Join(project, "agent")
		require.NoError(t, os.MkdirAll(serviceDir, 0o750))
		writeOptimizeConfig(t, serviceDir,
			"instruction_file: ../../../../outside-secret.md\n", "")

		_, _, err := AgentInstructionsFromProject(agentService(t, project, "agent", ""), "agent")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the project")
		assert.NotContains(t, err.Error(), "a file the project does not contain")
	})

	t.Run("reached through a symlink", func(t *testing.T) {
		root := t.TempDir()
		secret := filepath.Join(root, "outside-secret.md")
		require.NoError(t, os.WriteFile(secret, []byte("a file the project does not contain"), 0o600))

		project := filepath.Join(root, "proj")
		baseline := filepath.Join(project, "agent", ".agent_configs", "baseline")
		require.NoError(t, os.MkdirAll(baseline, 0o750))
		writeOptimizeConfig(t, filepath.Join(project, "agent"),
			"instruction_file: instructions.md\n", "")

		// A symlink is what git materializes on checkout, so it needs no more
		// privilege to commit than the `..` above.
		link := filepath.Join(baseline, "instructions.md")
		if err := os.Symlink(secret, link); err != nil {
			if runtime.GOOS == "windows" {
				t.Skip("creating a symlink on Windows needs a privilege this run does not have")
			}
			require.NoError(t, err)
		}

		_, _, err := AgentInstructionsFromProject(agentService(t, project, "agent", ""), "agent")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the project")
		assert.NotContains(t, err.Error(), "a file the project does not contain")
	})
}
