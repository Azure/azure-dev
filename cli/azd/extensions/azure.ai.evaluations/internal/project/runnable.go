// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import "azureaieval/internal/messages"

// ValidateRunnable refuses a declaration no run could carry out.
//
// One definition of what an eval has to say about itself, called on the way to
// deploying it and again when a run is built. Resolving an eval by name does
// not check any of this -- a lookup depends only on the name -- and a run
// reached by id has no declaration to check, so the run door is the first
// refusal as often as the config door is. Two hand-written copies drifted apart
// every time, on whichever axis was not tested at both ends.
//
// The errors carry no prefix. The caller says whether it has an index to name.
//
// What is not here is what the rest of the file, or the service, has to decide:
// whether a dataset or an evaluator is in its catalog, whether two evals are
// the same in substance, and what a published evaluator requires. The evaluator
// checks stay at the create door because they constrain what is created rather
// than what a run sends.
func ValidateRunnable(eval *Eval) error {
	if eval == nil {
		return messages.NoEvalToValidate()
	}
	// Two answers to where rows come from, and the file does not say which was
	// meant. Refused rather than ranked: settling it by which field is read
	// first sends a request that succeeds and grades the other one.
	if eval.Dataset != "" && eval.Source != nil {
		return messages.DatasetAndSourceDeclareTheSameThing()
	}
	if eval.MaxSamples < 0 {
		return messages.MaxSamplesNegative(eval.MaxSamples)
	}

	// The target is checked before the source, because the trace rule below
	// reads the target: without this, an eval with an unusable target is told
	// to name an agent on it, and told on the next run that the target it was
	// sent to name is a kind nothing can invoke.
	if eval.Target != nil {
		if eval.Target.Type != "" &&
			eval.Target.Type != TargetTypeAgent && eval.Target.Type != TargetTypeModel {
			return messages.TargetTypeNotSupported(eval.Target.Type, TargetTypeAgent, TargetTypeModel)
		}
		// A target with no name is scored as though nothing were invoked,
		// which is a different evaluation from the one that was written down.
		if eval.Target.Name == "" {
			return messages.TargetNameMissing()
		}
	}

	if eval.Source != nil {
		switch eval.Source.Type {
		case SourceTypeTraces:
			if TraceAgentName(eval.Source, eval.Target) == "" {
				// A model target is the one case where a target is present and
				// still no answer. Saying "or declare an agent target.name"
				// there reads as an invitation to relabel the deployment, which
				// produces a filter that matches no spans and reports nothing.
				if eval.Target != nil && eval.Target.Type == TargetTypeModel {
					return messages.TraceSourceCannotReadAModelTarget(eval.Target.Name)
				}
				return messages.TraceSourceNeedsAnAgent()
			}
		case SourceTypeResponses:
			if len(eval.Source.ResponseIDs) == 0 {
				return messages.ResponsesSourceNeedsResponseIDs()
			}
		case "":
			return messages.SourceTypeMissing()
		default:
			return messages.SourceTypeNotSupported(
				eval.Source.Type, SourceTypeTraces, SourceTypeResponses)
		}
		if _, _, err := ValidateSource(eval.Source); err != nil {
			return err
		}
	}

	switch eval.EvaluationLevel {
	case "", EvaluationLevelTurn, EvaluationLevelConversation:
	default:
		// Sent as run metadata, and anything that is not "conversation" is
		// read as turn-shaped, so a value nothing knows about grades the run at
		// a granularity the file did not ask for.
		return messages.EvaluationLevelNotSupported(
			eval.EvaluationLevel, EvaluationLevelTurn, EvaluationLevelConversation)
	}
	return nil
}
