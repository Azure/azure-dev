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
// What is not here needs the rest of the file to decide: whether a dataset or
// an evaluator is in its catalog, and whether two evals are the same in
// substance.
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

	if eval.Source != nil {
		switch eval.Source.Type {
		case SourceTypeTraces:
			if TraceAgentName(eval.Source, eval.Target) == "" {
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

	if eval.Target != nil {
		// The type is checked first because it is the thing that was written:
		// a target with an unsupported type and no name should be told about
		// the type rather than sent to add a name it cannot use.
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
	return nil
}
