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
	// Regeneration overwrites the file in place, so the entry only changes when
	// the artifact moved -- which UpsertCatalogEntry reports rather than rewrite.
	return updateCatalog(cmd, evalDir, "dataset", ref, "datasets", "file")
}

// addEvaluatorToCatalog records a generated evaluator in `evaluators:`.
func addEvaluatorToCatalog(cmd *cobra.Command, evalDir string, ref *project.ArtifactRef) error {
	if ref == nil {
		return nil
	}
	return updateCatalog(cmd, evalDir, "evaluator", ref, "evaluators", "source")
}

// checkCatalogEntryIsEditable refuses a name this command cannot rewrite in
// place without corrupting the entry.
//
// Four shapes reach this. A pure `$ref` has no name here at all, so the
// duplicate scan had nothing to match on and appended a second entry with the
// same name -- a collision that surfaced only on the next resolving read. A
// `$ref` carrying an overlay `name` does match, and updating it in place writes
// `source:` beside the directive, so resolution then produces a rubric and a
// source and the configuration is rejected for declaring it twice. An entry
// already carrying its rubric under `definition:`, and an entry pinned to a
// registered `version:`, both fail the same way with no include involved:
// recording the generated file leaves two rubrics, or a pin and a file, in one
// entry. Each of those is refused on the next read -- after the generation job
// has been billed and the file written, which is why they are refused here.
//
// Called twice: once by `buildGeneratePlans`, before anything is spent, and
// again here under the lock, which is the answer that counts. The pre-flight
// call is the one a reader notices, because it is the one that runs before
// they are charged for a rubric this command then cannot record.
//
// A configuration that will not resolve is left to the commands that resolve it:
// failing a generate over an unrelated broken include would be its own surprise.
func checkCatalogEntryIsEditable(evalDir string, asWritten *project.AuthoredConfig, kind, name string) error {
	section := catalogSection(kind)
	if entry, ok := asWritten.Entry(section, name); ok {
		switch {
		case entry.Ref != "":
			return messages.CatalogNameBehindAnInclude(kind, name)
		case kind == "dataset":
			// A dataset may hold a file and a version together; the two
			// refusals below are about an evaluator's single rubric.
			return nil
		case entry.HasDefinition:
			return messages.EvaluatorRubricWrittenInPlace(name)
		case entry.Version != "":
			return messages.EvaluatorPinnedToAVersion(name)
		}
		return nil
	}
	if !asWritten.HasUnnamedRef(section) {
		// Nothing here names it and no include could be hiding it, so there is
		// nothing this command cannot rewrite.
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

// catalogSection maps a command's word for a catalog to the key it lives under.
func catalogSection(kind string) string {
	if kind == "dataset" {
		return project.SectionDatasets
	}
	return project.SectionEvaluators
}

// catalogEntryShape is how an entry was written, for deciding whether this
// command may rewrite it.
type catalogEntryShape struct {
	// inlineRubric is an evaluator holding its rubric under `definition:`.
	inlineRubric bool
	// pinned is an evaluator naming a registered `version:`. A dataset may hold
	// a file and a version together; an evaluator may not.
	pinned bool
}

// refuseUneditableCatalogEntry is the pre-flight form of the check, for the
// callers that are about to spend money.
//
// It takes no lock, and is deliberately not the authoritative answer: the same
// check runs again under the lock before the entry is written. Its whole job is
// to fail early, so a reader whose entry cannot be rewritten hears it before a
// generation job runs rather than after it has been billed.
//
// A configuration that cannot be read is not this check's business either --
// the command that resolves it will say so with better context.
func refuseUneditableCatalogEntry(evalDir, kind, name string) error {
	cfg, err := project.ReadAuthoredConfig(evalDir)
	if err != nil || cfg == nil {
		return nil
	}
	return checkCatalogEntryIsEditable(evalDir, cfg, kind, name)
}

// catalogEntryShapeOf returns how the entry was written, and whether the
// configuration names it at all.
func catalogEntryShapeOf(cfg *project.EvalConfig, kind, name string) (catalogEntryShape, bool) {
	if cfg == nil {
		return catalogEntryShape{}, false
	}
	if kind == "dataset" {
		if _, ok := cfg.DatasetDeclaration(name); ok {
			return catalogEntryShape{}, true
		}
		return catalogEntryShape{}, false
	}
	if decl, ok := cfg.EvaluatorDeclaration(name); ok {
		return catalogEntryShape{
			inlineRubric: decl.Definition != nil,
			pinned:       decl.Version != "",
		}, true
	}
	return catalogEntryShape{}, false
}

// updateCatalog records the artifact in the configuration and writes it back.
//
// A missing configuration is created holding only the catalog. `generate` runs
// before `init` on the golden path, and a downloaded artifact nobody recorded
// is the one state that goes stale. The file it creates has no evals and no
// azure.yaml entry, so it stays inert until init wires one.
//
// The read and the write are separate on purpose. Decisions are made from the
// decoded configuration, because that is what the guard understands; the write
// edits the node tree, so a comment the author left, and any key these structs
// do not model, come back exactly as they were found.
func updateCatalog(
	cmd *cobra.Command,
	evalDir string,
	kind string,
	ref *project.ArtifactRef,
	sequence string,
	field string,
) error {
	// Held across the read and the write: two generates adding different
	// entries would otherwise both read the same state, and the second write
	// would drop the first one's entry while reporting success.
	unlock, err := project.LockEvalConfig(cmd.Context(), evalDir)
	if err != nil {
		return err
	}
	defer unlock()

	cfg, err := project.ReadAuthoredConfig(evalDir)
	if err != nil {
		return err
	}
	if err := checkCatalogEntryIsEditable(evalDir, cfg, kind, ref.Name); err != nil {
		return err
	}

	changed, created, err := project.UpsertCatalogEntry(evalDir, sequence, ref.Name, field, ref.Source)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	if !isJSON(cmd) {
		// Resolved, not the current name: the write goes back over a legacy file
		// when that is what the project has, and the line has to name the file it
		// actually wrote.
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
