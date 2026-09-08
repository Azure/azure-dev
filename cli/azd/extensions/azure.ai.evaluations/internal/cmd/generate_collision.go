// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"

	"azureaieval/internal/messages"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

// errGenerationCancelled is a caller answering a prompt with Cancel.
//
// A sentinel rather than a message, because cancelling is not a failure: the
// command says so and exits 0, the same as declining the final confirmation.
var errGenerationCancelled = errors.New("generation cancelled")

// What a caller can do about a name that is already taken.
const (
	collisionRegenerate = iota
	collisionRename
	collisionCancel
)

// collisionSuffixLimit bounds the search for a free `-2`, `-3` name.
//
// A caller who has thirty of these does not want a thirty-first proposed to
// them; something else has gone wrong and the name is worth typing out.
const collisionSuffixLimit = 30

// resolveArtifactCollision settles a generated name whose file is already there.
//
// Generation used to refuse outright. That is the right answer for a script and
// the wrong one for a person: the file is usually the artifact they generated
// last time, regenerating it is the common intent, and the refusal made them
// re-run the whole command with --force to say so. Worse, --force is the only
// way it offered, so the caller who actually wanted to keep both artifacts had
// to invent a name with no idea which ones were taken.
//
// Returns the name to generate under. An unchanged name means regenerate.
func resolveArtifactCollision(
	cmd *cobra.Command,
	kind, name, path string,
	force bool,
) (string, error) {
	if force {
		return name, nil
	}
	switch _, err := os.Stat(path); {
	case err == nil:
	case os.IsNotExist(err):
		return name, nil
	default:
		// A permission or I/O error read as "nothing there", so a billed job ran
		// and the write it was for failed afterwards.
		return "", messages.CheckingArtifactPath(filepath.ToSlash(path), err)
	}

	// Nobody to ask, so the refusal stands and names the flag that answers it.
	// A nil command is the same case: there is no prompt to reach.
	if cmd == nil || noPrompt(cmd) {
		return "", messages.ArtifactExists(filepath.ToSlash(path))
	}

	proposed := nextFreeArtifactName(name, path)
	choice, err := promptArtifactCollision(cmd, kind, name, path, proposed)
	if err != nil {
		return "", err
	}
	switch choice {
	case collisionRegenerate:
		return name, nil
	case collisionRename:
		if proposed == "" {
			return "", messages.NoFreeArtifactName(name)
		}
		return proposed, nil
	default:
		return "", errGenerationCancelled
	}
}

// promptArtifactCollision asks what to do about a name already in use.
func promptArtifactCollision(
	cmd *cobra.Command,
	kind, name, path, proposed string,
) (int, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return collisionCancel, messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	choices := []*azdext.SelectChoice{
		{Label: messages.RegenerateArtifactChoice(), Value: "regenerate"},
	}
	if proposed != "" {
		choices = append(choices, &azdext.SelectChoice{
			Label: messages.RenameArtifactChoice(proposed), Value: "rename",
		})
	}
	choices = append(choices, &azdext.SelectChoice{
		Label: messages.CancelGenerationChoice(), Value: "cancel",
	})

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         messages.ArtifactCollisionPrompt(kind, name, filepath.ToSlash(path)),
			Choices:         choices,
			SelectedIndex:   preselect(collisionRegenerate),
			EnableFiltering: filteringFor(len(choices)),
		},
	})
	if err != nil {
		return collisionCancel, messages.ResolvingArtifactCollision(err)
	}
	// An unanswered prompt is not consent to spend a job replacing a file. Value
	// is optional on the wire and an unset one arrives as 0, which here would be
	// "regenerate".
	if resp == nil || resp.Value == nil {
		return collisionCancel, nil
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(choices) {
		return collisionCancel, nil
	}
	// The rename row is absent when nothing is free, so the index is read off
	// the choices that were actually shown rather than off the constants.
	switch choices[index].Value {
	case "regenerate":
		return collisionRegenerate, nil
	case "rename":
		return collisionRename, nil
	default:
		return collisionCancel, nil
	}
}

// nextFreeArtifactName is the first `-2`, `-3` form whose file is free.
//
// Empty when none is, which is the caller's cue to stop offering the choice
// rather than propose a name that collides in turn.
func nextFreeArtifactName(name, path string) string {
	dir, file := filepath.Split(path)
	ext := filepath.Ext(file)
	for n := 2; n <= collisionSuffixLimit; n++ {
		candidate := name + "-" + strconv.Itoa(n)
		if _, err := os.Stat(filepath.Join(dir, candidate+ext)); os.IsNotExist(err) {
			return candidate
		}
	}
	return ""
}
