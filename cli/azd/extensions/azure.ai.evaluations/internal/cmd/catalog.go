// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"

	"azureaieval/internal/project"

	"github.com/spf13/cobra"
)

// Generation writes the artifact and then names it in the configuration, so
// what it produced is referenceable without a hand edit. Only the catalogs are
// touched: which evals use the artifact is the author's decision, and `init` is
// the command that makes it.

// addDatasetToCatalog records a generated dataset in `datasets:`.
func addDatasetToCatalog(cmd *cobra.Command, evalDir string, ref *project.ArtifactRef) error {
	if ref == nil {
		return nil
	}
	return updateCatalog(cmd, evalDir, "dataset", ref, func(cfg *project.EvalConfig) bool {
		for i := range cfg.Datasets {
			if cfg.Datasets[i].Name == ref.Name {
				// Regeneration overwrites the file in place, so the entry only
				// changes when the artifact moved.
				if cfg.Datasets[i].Source == ref.Source {
					return false
				}
				cfg.Datasets[i].Source = ref.Source
				return true
			}
		}
		cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{
			Name:   ref.Name,
			Source: ref.Source,
		})
		return true
	})
}

// addEvaluatorToCatalog records a generated evaluator in `evaluators:`.
func addEvaluatorToCatalog(cmd *cobra.Command, evalDir string, ref *project.ArtifactRef) error {
	if ref == nil {
		return nil
	}
	return updateCatalog(cmd, evalDir, "evaluator", ref, func(cfg *project.EvalConfig) bool {
		for i := range cfg.Evaluators {
			if cfg.Evaluators[i].Name == ref.Name {
				if cfg.Evaluators[i].Source == ref.Source {
					return false
				}
				cfg.Evaluators[i].Source = ref.Source
				return true
			}
		}
		cfg.Evaluators = append(cfg.Evaluators, project.EvaluatorDecl{
			Name:   ref.Name,
			Source: ref.Source,
		})
		return true
	})
}

// updateCatalog applies a change to the configuration and writes it back.
//
// A missing configuration is created holding only the catalog. `generate` runs
// before `init` on the golden path, and a downloaded artifact nobody recorded
// is the one state that goes stale. The file it creates has no evals and no
// azure.yaml entry, so it stays inert until init wires one.
func updateCatalog(
	cmd *cobra.Command,
	evalDir string,
	kind string,
	ref *project.ArtifactRef,
	apply func(*project.EvalConfig) bool,
) error {
	cfg, err := project.OpenEvalConfig(evalDir)
	if err != nil {
		return err
	}
	created := cfg == nil
	if created {
		cfg = &project.EvalConfig{}
	}
	if !apply(cfg) {
		return nil
	}

	if err := project.SaveEvalConfig(evalDir, cfg); err != nil {
		return err
	}
	if !isJSON(cmd) {
		path := filepath.ToSlash(project.EvalConfigPath(evalDir))
		if created {
			fmt.Fprintf(cmd.OutOrStdout(),
				"%s Created %s with the catalog entry\n", doneMark, path)
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"%s Added %s %s to %s\n", doneMark, kind, describeArtifact(ref), path)
	}
	return nil
}

// describeArtifact names what was recorded, with the published version when the
// job reported one, so a reader can pin it without going to look.
//
// Single-quoted to match the spec's transcripts; the surrounding Done: lines
// carry bare values, but a dataset name can hold a space and these cannot.
func describeArtifact(ref *project.ArtifactRef) string {
	if ref.Version == "" || ref.Version == "latest" {
		return fmt.Sprintf("'%s'", ref.Name)
	}
	return fmt.Sprintf("'%s' (version %s)", ref.Name, ref.Version)
}
