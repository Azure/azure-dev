// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
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
