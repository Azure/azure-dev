// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"slices"
	"testing"
)

func TestRootCommand_PublicPreviewCommandsVisible(t *testing.T) {
	cmd := NewRootCommand()

	var visible []string
	for _, sub := range cmd.Commands() {
		if !sub.Hidden {
			visible = append(visible, sub.Name())
		}
	}

	for _, name := range []string{
		"add",
		"code",
		"delete",
		"deploy",
		"doctor",
		"endpoint",
		"eval",
		"files",
		"init",
		"invoke",
		"monitor",
		"optimize",
		"pack",
		"publish",
		"run",
		"sample",
		"sessions",
		"show",
	} {
		if !slices.Contains(visible, name) {
			t.Fatalf("expected visible root subcommand %q in %v", name, visible)
		}
	}
}
