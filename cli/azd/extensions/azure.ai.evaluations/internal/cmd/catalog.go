// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"path/filepath"

	"azureaieval/internal/messages"
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
				if cfg.Datasets[i].File == ref.Source {
					return false
				}
				cfg.Datasets[i].File = ref.Source
				return true
			}
		}
		cfg.Datasets = append(cfg.Datasets, project.DatasetDecl{
			Name: ref.Name,
			File: ref.Source,
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

// checkCatalogEntryIsEditable refuses a name this command cannot rewrite in
// place without corrupting the entry.
//
// Three shapes reach this. A pure `$ref` has no name here at all, so the
// duplicate scan had nothing to match on and appended a second entry with the
// same name -- a collision that surfaced only on the next resolving read. A
// `$ref` carrying an overlay `name` does match, and updating it in place writes
// `source:` beside the directive, so resolution then produces a rubric and a
// source and the configuration is rejected for declaring it twice. An entry
// already carrying its rubric under `definition:` fails that same way with no
// include involved, because recording the generated file leaves both in one
// entry -- and it fails after the generation job has been billed and the file
// written, which is why it is refused here rather than left to the next read.
//
// A configuration that will not resolve is left to the commands that resolve it:
// failing a generate over an unrelated broken include would be its own surprise.
func checkCatalogEntryIsEditable(evalDir string, asWritten *project.EvalConfig, kind, name string) error {
	if entry, ok := catalogEntryShapeOf(asWritten, kind, name); ok {
		switch {
		case entry.ref != "":
			return messages.CatalogNameBehindAnInclude(kind, name)
		case entry.inlineRubric:
			return messages.EvaluatorRubricWrittenInPlace(name)
		}
		return nil
	}
	resolved, err := project.OpenEvalConfig(evalDir)
	if err != nil || resolved == nil {
		return nil
	}
	if _, ok := catalogEntryShapeOf(resolved, kind, name); ok {
		return messages.CatalogNameBehindAnInclude(kind, name)
	}
	return nil
}

// catalogEntryShape is how an entry was written, for deciding whether this
// command may rewrite it.
type catalogEntryShape struct {
	// ref is the `$ref` directive the entry carries, empty when it is written
	// out here.
	ref string
	// inlineRubric is an evaluator holding its rubric under `definition:`.
	inlineRubric bool
}

// catalogEntryShapeOf returns how the entry was written, and whether the
// configuration names it at all.
func catalogEntryShapeOf(cfg *project.EvalConfig, kind, name string) (catalogEntryShape, bool) {
	if cfg == nil {
		return catalogEntryShape{}, false
	}
	if kind == "dataset" {
		if decl, ok := cfg.DatasetDeclaration(name); ok {
			return catalogEntryShape{ref: decl.Ref}, true
		}
		return catalogEntryShape{}, false
	}
	if decl, ok := cfg.EvaluatorDeclaration(name); ok {
		return catalogEntryShape{ref: decl.Ref, inlineRubric: decl.Definition != nil}, true
	}
	return catalogEntryShape{}, false
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
	// Held across the read and the write: two generates adding different
	// entries would otherwise both read the same state, and the second write
	// would drop the first one's entry while reporting success.
	unlock, err := project.LockEvalConfig(cmd.Context(), evalDir)
	if err != nil {
		return err
	}
	defer unlock()

	cfg, err := project.OpenEvalConfigForEdit(evalDir)
	if err != nil {
		return err
	}
	created := cfg == nil
	if created {
		cfg = &project.EvalConfig{}
	}
	if err := checkCatalogEntryIsEditable(evalDir, cfg, kind, ref.Name); err != nil {
		return err
	}
	if !apply(cfg) {
		return nil
	}

	if err := project.SaveEvalConfig(evalDir, cfg); err != nil {
		return err
	}
	if !isJSON(cmd) {
		// Resolved, not the current name: SaveEvalConfig writes back over a
		// legacy file when that is what the project has, and the line has to
		// name the file it actually wrote.
		resolved, err := project.ResolveEvalConfigPath(evalDir)
		if err != nil {
			return err
		}
		path := filepath.ToSlash(resolved)
		if created {
			fmt.Fprint(cmd.OutOrStdout(), messages.CreatedCatalogFile(path))
		}
		fmt.Fprint(cmd.OutOrStdout(),
			messages.AddedToCatalog(kind, describeArtifact(ref), path))
	}
	return nil
}

// describeArtifact names what was recorded, with the published version when the
// job reported one, so a reader can pin it without going to look.
//
// Single-quoted to match the spec's transcripts; the surrounding Done: lines
// carry bare values, but a dataset name can hold a space and these cannot.
func describeArtifact(ref *project.ArtifactRef) string {
	return messages.ArtifactDescription(ref.Name, ref.Version)
}
