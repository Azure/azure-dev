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
// Unlike show's complete service document, a rubric download contains the
// editable definition. It refuses to replace an existing file without --force.
func newEvaluatorDownloadCommand() *cobra.Command {
	a := &evaluatorDownloadAction{}

	cmd := &cobra.Command{
		Use:   "download <name>",
		Short: "Download an editable evaluator rubric.",
		Long: "Download an editable evaluator rubric.\n\n" +
			"Rubrics contain dimensions, weights, and the pass threshold, without service-generated wiring.\n" +
			"Other evaluator kinds retain their complete document.\n" +
			"Use evaluator show -o json for the full service response.\n" +
			"Publishing with evaluator update preserves existing catalog metadata unless the input explicitly replaces it.",
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

// evaluatorDocument uses the same editable rubric shape as generation.
// Unrecognized definitions retain their complete document.
func evaluatorDocument(raw json.RawMessage) []byte {
	var envelope struct {
		Definition json.RawMessage `json:"definition"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		var definition struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(envelope.Definition, &definition) == nil &&
			(definition.Type == "" || definition.Type == rubricDefinitionType) {
			if editable, ok := editableRubric(envelope.Definition); ok {
				return editable
			}
		}
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return raw
	}
	return append(indented.Bytes(), '\n')
}
