// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"os"
	"os/exec"
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

	// A junction is the Windows escape, and it is the cheaper of the two:
	// mklink /J needs neither elevation nor Developer Mode, while the symlink
	// above needs one of them. Go reads a junction as an irregular file rather
	// than a link, so path resolution refuses it while the read walks through.
	t.Run("reached through a directory junction", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("junctions are a Windows reparse point")
		}

		root := t.TempDir()
		outside := filepath.Join(root, "outside")
		require.NoError(t, os.MkdirAll(outside, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(outside, "instructions.md"),
			[]byte("a file the project does not contain"), 0o600))

		project := filepath.Join(root, "proj")
		serviceDir := filepath.Join(project, "agent")
		require.NoError(t, os.MkdirAll(serviceDir, 0o750))
		writeOptimizeConfig(t, serviceDir, "instruction_file: elsewhere/instructions.md\n", "")

		// The pointer stays inside the project as written; the directory it
		// names is the reparse point.
		junction := filepath.Join(serviceDir, ".agent_configs", "baseline", "elsewhere")
		//nolint:gosec // fixed arguments, both paths built by this test
		out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, outside).CombinedOutput()
		if err != nil {
			t.Skipf("could not create a junction: %v: %s", err, out)
		}

		_, _, err = AgentInstructionsFromProject(agentService(t, project, "agent", ""), "agent")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "outside the project")
		assert.NotContains(t, err.Error(), "a file the project does not contain")
	})
}
