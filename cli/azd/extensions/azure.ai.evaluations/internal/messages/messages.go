// Package messages holds every string this extension shows a user.
//
// One file, so the whole voice of the CLI can be reviewed in one sitting and a
// wording change never has to be hunted through the command tree. Nothing here
// imports anything from the extension, so any package can use it.
//
// Conventions, so the set stays consistent:
//
//   - Errors state what went wrong and, where there is one, the way out.
//     Lowercase, no trailing period: azd renders them after "ERROR: ".
//   - A name the user chose is quoted with %q; an identifier the service
//     assigned is not, because it is already unmistakable.
//   - Progress and success lines are sentences with a capital and no period.
//   - Nothing here decides *whether* to print. That stays at the call site.
package messages

import (
	"errors"
	"fmt"
)

// ---------------------------------------------------------------------------
// Running an eval
// ---------------------------------------------------------------------------

// NoEvalToRun reports a run with nothing resolved to run.
func NoEvalToRun() error {
	return errors.New("no eval to run")
}

// EvalHasNoDataset reports an eval whose rows cannot be located.
//
// Named separately from the traces and responses cases because the way out is
// different: this one is answered by a dataset, not by a source block.
func EvalHasNoDataset(eval string) error {
	return fmt.Errorf(
		"eval %q references no dataset and declares no source:. Add a dataset: to "+
			"score rows you supply, or a source: to score traces or stored responses",
		eval)
}

// DatasetFileEmpty reports a local dataset file that parsed but held no rows.
func DatasetFileEmpty(path string) error {
	return fmt.Errorf("dataset file %q has no rows", path)
}

// TracesNeedAgentName reports a trace-backed eval that does not say whose
// traces to read.
func TracesNeedAgentName(eval string) error {
	return fmt.Errorf(
		"eval %q reads traces but does not say whose. Set source.agent_name to the "+
			"agent whose conversations should be evaluated",
		eval)
}

// ResponsesNeedIDs reports a stored-response eval with nothing to retrieve.
func ResponsesNeedIDs(eval string) error {
	return fmt.Errorf(
		"eval %q evaluates stored responses but lists none. Set source.response_ids",
		eval)
}

// ---------------------------------------------------------------------------
// Generation
// ---------------------------------------------------------------------------

// GenerationModelRequired reports a generation with no deployment to run on.
//
// Reached only when the target agent could not supply one either, so the flag
// is the whole of the way out.
func GenerationModelRequired() error {
	return errors.New("a model deployment is required to generate: pass --generation-model")
}
