// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"azureaieval/internal/messages"
	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
)

type evaluatorDownloadAction struct {
	cmd       *cobra.Command
	endpoint  string
	version   string
	outputDir string
	outFile   string
	force     bool
	name      string
}

// newEvaluatorDownloadCommand builds `evaluator download <name>`.
//
// `show --output-file` writes the same document, but it is the adoption path:
// reconciliation points it at a file to overwrite, so it replaces what it finds.
// A download is the opposite promise -- it refuses to destroy what is already
// there without --force -- and it names the file itself, which is what makes
// fetching several evaluators in one directory work.
func newEvaluatorDownloadCommand() *cobra.Command {
	a := &evaluatorDownloadAction{}

	cmd := &cobra.Command{
		Use:   "download <name>",
		Short: "Download a registered evaluator version's definition.",
		Example: "# Download the latest rubric to a local file\n" +
			"  azd ai eval evaluator download my-rubric --output-file rubric.json",
		Args: requiredArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.cmd, a.name = cmd, args[0]
			return a.Run()
		},
	}

	cmd.Flags().StringVar(&a.version, "version", "",
		"Version to download. Omit for the latest, which is reported.")
	cmd.Flags().StringVar(&a.outputDir, "output-dir", "",
		"Directory to write into. Defaults to the current directory.")
	cmd.Flags().StringVar(&a.outFile, "output-file", "",
		"Exact path to write.")
	cmd.Flags().BoolVar(&a.force, "force", false,
		"Overwrite a file that already exists.")
	cmd.Flags().StringVar(&a.endpoint, "project-endpoint", "", "Foundry project endpoint.")
	return cmd
}

func (a *evaluatorDownloadAction) Run() error {
	if !validLookupName(a.name) {
		return messages.InvalidEvaluatorName(a.name)
	}
	// Refused before the round trip: both name a destination, and honouring
	// either one silently would write somewhere the caller did not ask for.
	if a.outFile != "" && a.outputDir != "" {
		return messages.OutputFileAndDirBothGiven()
	}

	ctx := a.cmd.Context()
	ec, err := newEvalContext(ctx, a.endpoint)
	if err != nil {
		return err
	}
	defer ec.Close()

	return a.download(ctx, ec)
}

// download fetches the definition and puts it on disk, split from Run so a test
// can drive it against a service it controls.
func (a *evaluatorDownloadAction) download(ctx context.Context, ec *evalContext) error {
	version := a.version
	if version == "" {
		latest, err := ec.evalClient.LatestEvaluatorVersion(ctx, a.name, ProjectEndpointAPIVersion)
		if err != nil {
			if eval_api.IsNotFound(err) {
				return messages.EvaluatorNotFound(a.name)
			}
			return err
		}
		version = latest
	}

	raw, err := ec.evalClient.GetEvaluatorRaw(ctx, a.name, version, ProjectEndpointAPIVersion)
	if err != nil {
		if eval_api.IsNotFound(err) {
			return messages.EvaluatorNotFound(a.name)
		}
		return messages.ReadingEvaluator(a.name, err)
	}

	path, err := a.destination(version)
	if err != nil {
		return err
	}
	if err := refuseExisting(path, a.force); err != nil {
		return err
	}
	if err := writeFileAtomically(path, bytes.NewReader(evaluatorDocument(raw)), a.force); err != nil {
		return err
	}

	if isJSON(a.cmd) {
		return emitJSON(a.cmd.OutOrStdout(), map[string]any{
			"evaluator": a.name,
			"version":   version,
			"path":      path,
		})
	}
	out := a.cmd.OutOrStdout()
	fmt.Fprint(out, messages.DownloadedEvaluator(a.name, version, path))
	if prefix := ec.portalPrefix(ctx); prefix != nil {
		writePortalLink(out, prefix.EvaluatorURL(a.name, version))
	}
	return nil
}

// destination is where the definition lands.
func (a *evaluatorDownloadAction) destination(version string) (string, error) {
	if a.outFile != "" {
		return a.outFile, nil
	}
	dir := a.outputDir
	if dir == "" {
		dir = "."
	}
	leaf, err := derivedLeafName(a.name, version, ".json")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, leaf), nil
}

// evaluatorDocument is the service's document, indented when it is JSON.
//
// Only the indentation is this command's: a field this CLI does not model is
// still the evaluator's, and dropping it would hand back something that no
// longer round-trips through `evaluator update`.
func evaluatorDocument(raw json.RawMessage) []byte {
	var pretty any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		return raw
	}
	indented, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return raw
	}
	return append(indented, '\n')
}
