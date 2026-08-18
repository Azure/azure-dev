// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One definition, two reporters. The configuration and the request builder both
// have to decide whether a declaration can run, and every round they decided it
// separately they drifted: one accepted what the other refused, and which rules
// applied depended on which door the eval came through.
//
// The errors carry no prefix of their own -- the caller adds what it knows --
// so the rule can be stated once and reported from either side.
func TestValidateRunnable_RefusesWhatNoRunCouldCarryOut(t *testing.T) {
	dataset := func(e Eval) Eval { e.Name, e.Dataset = "e", "d"; return e }

	cases := []struct {
		name    string
		eval    Eval
		wantErr string
	}{
		{
			"rows from two places",
			Eval{Dataset: "d", Source: &SourceDecl{Type: SourceTypeTraces, AgentName: "a"}},
			"declare one",
		},
		{"a negative cap", dataset(Eval{MaxSamples: -1}), "max_samples cannot be negative"},
		{
			"a target naming nothing",
			dataset(Eval{Target: &Target{Type: TargetTypeAgent}}),
			"target.name is required",
		},
		{
			"a target nothing can invoke",
			dataset(Eval{Target: &Target{Type: "prompt", Name: "x"}}),
			`target.type "prompt" is not supported`,
		},
		{
			"a source that does not say what it reads",
			Eval{Name: "e", Source: &SourceDecl{}},
			"source.type is required",
		},
		{
			"a source nothing can read",
			Eval{Name: "e", Source: &SourceDecl{Type: "trace"}},
			`source.type "trace" is not supported`,
		},
		{
			"a trace source naming no agent",
			Eval{Name: "e", Source: &SourceDecl{Type: SourceTypeTraces}},
			"source.agent_name is required",
		},
		{
			"a responses source listing nothing",
			Eval{Name: "e", Source: &SourceDecl{Type: SourceTypeResponses}},
			"source.response_ids is required",
		},
		{
			"a window the source cannot use",
			Eval{Name: "e", Source: &SourceDecl{
				Type: SourceTypeTraces, AgentName: "a", LookbackHours: -1,
			}},
			"cannot be negative",
		},
		{
			// Sent as run metadata, and anything that is not "conversation" is
			// read as turn-shaped, so an unrecognised value grades the run at a
			// granularity the file did not ask for.
			"a granularity nothing scores at",
			dataset(Eval{EvaluationLevel: "sentence"}),
			`evaluation_level "sentence" is invalid`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRunnable(&tc.eval)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			// The caller says where the error came from. A prefix here would
			// be repeated by one door and wrong at the other.
			assert.NotContains(t, err.Error(), "evals[")
			assert.NotContains(t, err.Error(), `eval "`)
		})
	}
}

// A trace eval pointed at a model deployment is the one case where a target is
// present and still answers nothing. The general advice reads as an invitation
// to relabel the deployment as an agent, which produces a filter matching no
// spans and a run that reports nothing.
func TestValidateRunnable_SaysWhyAModelTargetIsNotAnAgent(t *testing.T) {
	err := ValidateRunnable(&Eval{
		Name:   "e",
		Source: &SourceDecl{Type: SourceTypeTraces},
		Target: &Target{Type: TargetTypeModel, Name: "gpt-4o-mini"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a model deployment")
	assert.NotContains(t, err.Error(), "declare an agent target.name",
		"advice that leaves the eval wrong in a way nothing reports")
}

// An eval wrong in two ways is told about the one that cannot be worked around,
// so following the advice does not lead straight back here.
func TestValidateRunnable_ReportsTheUnusableTargetBeforeTheRuleThatReadsIt(t *testing.T) {
	err := ValidateRunnable(&Eval{
		Name:   "e",
		Source: &SourceDecl{Type: SourceTypeTraces},
		Target: &Target{Type: "prompt"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not supported")
	assert.NotContains(t, err.Error(), "agent_name",
		"naming an agent on a target nothing can invoke fixes nothing")
}

// A declaration that says nothing contradictory passes, whichever shape it is.
func TestValidateRunnable_Accepts(t *testing.T) {
	for name, eval := range map[string]Eval{
		"a dataset scored as it stands": {Name: "e", Dataset: "d"},
		"a dataset with an agent target": {
			Name: "e", Dataset: "d", Target: &Target{Type: TargetTypeAgent, Name: "a"},
		},
		"a dataset with an untyped target": {
			Name: "e", Dataset: "d", Target: &Target{Name: "a"},
		},
		"traces filtered by name": {
			Name: "e", Source: &SourceDecl{Type: SourceTypeTraces, AgentName: "a"},
		},
		"traces named by the target": {
			Name:   "e",
			Source: &SourceDecl{Type: SourceTypeTraces},
			Target: &Target{Type: TargetTypeAgent, Name: "a"},
		},
		"stored responses": {
			Name:   "e",
			Source: &SourceDecl{Type: SourceTypeResponses, ResponseIDs: []string{"resp_1"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, ValidateRunnable(&eval))
		})
	}
}
