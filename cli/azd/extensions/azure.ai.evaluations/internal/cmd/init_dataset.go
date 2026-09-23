// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaieval/internal/messages"
	"azureaieval/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

func resolveInitDataset(
	cmd *cobra.Command, location string, answers *initAnswers, cfg *project.EvalConfig,
) error {
	ctx := commandContext(cmd)
	problem := validateInitDataset(ctx, location, *answers, cfg)
	// Bound retries like the eval-name prompt, without losing the last row error.
	for range 8 {
		if problem == nil || noPrompt(cmd) {
			return problem
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), messages.InitDatasetRejected(problem))
		dataset, err := promptDatasetReference(cmd)
		if err != nil {
			return err
		}
		answers.datasetRef = dataset
		problem = validateInitDataset(ctx, location, *answers, cfg)
	}
	return problem
}

func validateInitDataset(
	ctx context.Context, location string, answers initAnswers, cfg *project.EvalConfig,
) error {
	if answers.source == initSourceTraces {
		return nil
	}
	path := answers.datasetRef
	if looksLikeLocalDataset(path) {
		if _, err := resolveInitLocalDataset(location, path, cfg); err != nil {
			return err
		}
	} else if answers.simulation != nil {
		decl, err := project.ReadAuthoredDataset(location, answers.datasetRef)
		if err != nil {
			return err
		}
		if decl == nil {
			return nil // Registered names have no local rows for init to inspect.
		}
		// A ref-only declaration takes its name from the included file. Keep
		// the name in the add-only accumulator without inlining its content.
		if !declaresDataset(cfg, decl.Name) {
			cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{Name: decl.Name})
		}
		path = decl.File
	}
	if answers.simulation == nil || path == "" {
		return nil
	}
	group := &project.Eval{Name: answers.evalName, Simulation: answers.simulation}
	_, err := inspectJSONL(ctx, path, func(row map[string]any, index int) error {
		return refuseUnusableSeedRow(group, row, index)
	})
	return err
}

// resolveInitLocalDataset binds the file to its eventual catalog name before
// validation or planning can accept a file that add-only authoring would ignore.
func resolveInitLocalDataset(location, path string, cfg *project.EvalConfig) (project.DatasetDecl, error) {
	requested := project.DatasetDecl{
		Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		File: relativeToConfig(path, location),
	}
	info, err := os.Stat(path)
	if err != nil {
		return project.DatasetDecl{}, messages.DatasetFileNotFound(path, err)
	}
	existing, err := project.ReadAuthoredDataset(location, requested.Name)
	if err != nil {
		return project.DatasetDecl{}, err
	}
	if existing == nil {
		if decl, ok := cfg.DatasetDeclaration(requested.Name); ok {
			existing = &project.DatasetDecl{
				Name: decl.Name, File: project.ResolveSource(project.EvalDirOf(location), decl.File),
			}
		}
	}
	if existing == nil {
		return requested, nil
	}
	if existing.File != "" {
		other, err := os.Stat(existing.File)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return project.DatasetDecl{}, messages.DatasetFileNotFound(existing.File, err)
		}
		if err == nil && os.SameFile(info, other) {
			// A ref-only entry must also be counted as existing by add-only planning.
			if !declaresDataset(cfg, existing.Name) {
				cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{Name: existing.Name})
			}
			return requested, nil
		}
	}
	return project.DatasetDecl{}, messages.InitDatasetFileConflict(requested.Name, path)
}

// resolveDataset settles which dataset a dataset-backed evaluation grades.
//
// init used to fill this gap by declaring a dataset it would generate later,
// which wrote an eval nothing satisfied. Refusing instead is correct but ends
// the one command a developer runs first on an error, so where there is a
// person to ask, it asks: which of the declared datasets, or -- with none
// declared -- what to point at.
//
// Under --no-prompt there is nobody to ask, so the flag is named. The answer is
// only a proposal: `planScaffold` re-reads the configuration under the lock and
// validates whatever comes back, so a `generate` that landed in between is not
// overwritten by a menu drawn before it.
func resolveDataset(cmd *cobra.Command, cfg *project.EvalConfig, flag string) (string, error) {
	if strings.TrimSpace(flag) != "" {
		return flag, nil
	}

	declared := datasetNames(cfg)
	switch len(declared) {
	case 1:
		// The one declaration in the file is not a guess.
		return declared[0], nil
	case 0:
		if noPrompt(cmd) {
			return "", messages.DatasetSourceNeedsADataset()
		}
		return promptDatasetReference(cmd)
	}

	if noPrompt(cmd) {
		return "", messages.AmbiguousDeclaredDataset(declared)
	}
	return promptDeclaredDataset(cmd, declared)
}

// promptDeclaredDataset asks which of the configuration's datasets to grade.
func promptDeclaredDataset(cmd *cobra.Command, declared []string) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	choices := make([]*azdext.SelectChoice, 0, len(declared))
	for i := range declared {
		choices = append(choices, &azdext.SelectChoice{Label: declared[i], Value: declared[i]})
	}

	resp, err := azdClient.Prompt().Select(commandContext(cmd), &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message:         messages.SelectDatasetPrompt(),
			Choices:         choices,
			EnableFiltering: filteringFor(len(choices)),
		},
	})
	if err != nil {
		return "", messages.SelectingDataset(err)
	}
	// Value is optional on the wire, so an unset one arrives as 0 from GetValue
	// and would read as the first dataset rather than as no answer.
	if resp == nil || resp.Value == nil {
		return "", messages.AmbiguousDeclaredDataset(declared)
	}
	index := int(resp.GetValue())
	if index < 0 || index >= len(declared) {
		return "", messages.AmbiguousDeclaredDataset(declared)
	}
	return declared[index], nil
}

// promptDatasetReference asks what to grade when the configuration declares
// nothing to offer or a local dataset needs correction.
//
// It takes a path or a registered name rather than a list, because the two
// things it could list are both service calls init does not make: the datasets
// registered in the project, and the files on disk that happen to be evaluation
// rows.
func promptDatasetReference(cmd *cobra.Command) (string, error) {
	azdClient, err := azdext.NewAzdClient()
	if err != nil {
		return "", messages.ConnectingToAzd(err)
	}
	defer azdClient.Close()

	resp, err := azdClient.Prompt().Prompt(commandContext(cmd), &azdext.PromptRequest{
		Options: &azdext.PromptOptions{
			Message:         messages.EnterDatasetPrompt(),
			HelpMessage:     messages.EnterDatasetHelp(),
			Placeholder:     "./evals/datasets/golden.jsonl",
			Required:        true,
			RequiredMessage: messages.DatasetIsRequired(),
		},
	})
	if err != nil {
		return "", messages.SelectingDataset(err)
	}
	// An empty answer is the question unanswered, and continuing on it would
	// write the reference that has no dataset behind it all over again.
	if resp == nil || strings.TrimSpace(resp.GetValue()) == "" {
		return "", messages.DatasetSourceNeedsADataset()
	}
	return strings.TrimSpace(resp.GetValue()), nil
}
