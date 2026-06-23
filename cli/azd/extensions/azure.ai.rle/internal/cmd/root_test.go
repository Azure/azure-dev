// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestNewRootCommandIncludesExpectedCommands(t *testing.T) {
	rootCmd := NewRootCommand()

	for _, commandName := range []string{"create", "modify", "version", "metadata"} {
		if command, _, err := rootCmd.Find([]string{commandName}); err != nil || command.Name() != commandName {
			t.Fatalf("expected command %q to be registered", commandName)
		}
	}
}
