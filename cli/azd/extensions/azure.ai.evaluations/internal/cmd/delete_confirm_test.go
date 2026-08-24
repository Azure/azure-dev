// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// deleteCommands is every verb that removes something from the project.
//
// `job delete` is absent on purpose: it discards a record of work already
// finished, and says so in its own help. The artifact the job produced is a
// registered version that the delete does not touch.
func deleteCommands() map[string]*cobra.Command {
	return map[string]*cobra.Command{
		"dataset delete":   newDatasetDeleteCommand(),
		"evaluator delete": newEvaluatorDeleteCommand(),
		"eval delete":      newEvalDeleteCommand(),
		"run delete":       newRunDeleteCommand(),
	}
}

// TestDeletesAskFirst is the parity test. The extension pair is advertised as
// answering the same way for the same verb, so a delete that removes published
// data without asking is a difference a reader finds by losing something.
func TestDeletesAskFirst(t *testing.T) {
	for name, cmd := range deleteCommands() {
		t.Run(name, func(t *testing.T) {
			flag := cmd.Flags().Lookup("force")
			if flag == nil {
				t.Fatalf("%s has no --force, so it cannot be asking first", name)
			}
			if flag.Value.Type() != "bool" {
				t.Errorf("--force on %s is %s, want bool", name, flag.Value.Type())
			}
			if flag.DefValue != "false" {
				t.Errorf("--force on %s defaults to %s, want false", name, flag.DefValue)
			}
		})
	}
}

// TestDeleteHelpSaysHowToSkipTheQuestion covers the scripted reader: they meet
// the refusal under --no-prompt, and the way out has to be in the help they
// already have rather than in a message they have to search for.
func TestDeleteHelpSaysHowToSkipTheQuestion(t *testing.T) {
	for name, cmd := range deleteCommands() {
		t.Run(name, func(t *testing.T) {
			help := cmd.Long + "\n" + cmd.Short
			if !strings.Contains(help, "--force") {
				t.Errorf("help for %s never mentions --force:\n%s", name, help)
			}
		})
	}
}

// TestDeleteRefusesWithoutAWayToAsk pins the refusal itself. A command that
// cannot ask and was not told to go ahead must stop, and must name the flag
// that lets the reader proceed -- an error that only says "cannot prompt"
// leaves a script broken with nothing to change.
//
// JSON output counts as cannot-ask: a prompt written into a document a script
// is parsing is a hang, not a question.
func TestDeleteRefusesWithoutAWayToAsk(t *testing.T) {
	for _, how := range []string{"no-prompt", "json"} {
		t.Run(how, func(t *testing.T) {
			err := confirmDelete(cannotAsk(t, how), nil, "dataset scores version 1", false)
			if err == nil {
				t.Fatal("a delete that cannot ask went ahead anyway")
			}
			if !strings.Contains(err.Error(), "--force") {
				t.Errorf("refusal does not name --force: %v", err)
			}
			if !strings.Contains(err.Error(), "scores") {
				t.Errorf("refusal does not name what it refused to delete: %v", err)
			}
		})
	}
}

// cannotAsk builds a command in each of the two states where no one can answer.
func cannotAsk(t *testing.T, how string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "delete"}
	cmd.Flags().Bool("no-prompt", false, "")
	cmd.Flags().StringP("output", "o", "", "")
	if err := cmd.Flags().Set(map[string]string{
		"no-prompt": "no-prompt",
		"json":      "output",
	}[how], map[string]string{
		"no-prompt": "true",
		"json":      "json",
	}[how]); err != nil {
		t.Fatalf("setting up the %s case: %v", how, err)
	}
	return cmd
}

// TestForceSkipsTheQuestion is the other half: --force must not consult the
// prompt at all, which a nil client proves -- reaching for it would panic.
func TestForceSkipsTheQuestion(t *testing.T) {
	cmd := &cobra.Command{Use: "delete"}
	cmd.Flags().Bool("no-prompt", false, "")
	if err := confirmDelete(cmd, nil, "dataset scores version 1", true); err != nil {
		t.Fatalf("--force still refused the delete: %v", err)
	}
}
