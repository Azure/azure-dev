// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"

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
			// read as turn-shaped, so a value nothing knows about grades the run
			// at a granularity the file did not ask for.
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
		"a simulation over registered seeds": {
			Name:            "e",
			Dataset:         "d",
			EvaluationLevel: EvaluationLevelConversation,
			Target:          &Target{Type: TargetTypeAgent, Name: "a"},
			Simulation:      &Simulation{Model: "gpt-4o-mini", NumConversations: 1, MaxTurns: 5},
		},
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, ValidateRunnable(&eval))
		})
	}
}

// A declaration that satisfies every simulation rule, so each case below can
// break exactly one thing and attribute the refusal to it.
func runnableSimulation() Eval {
	return Eval{
		Name:            "retail-multiturn",
		Dataset:         "d",
		EvaluationLevel: EvaluationLevelConversation,
		Target:          &Target{Type: TargetTypeAgent, Name: "hero-agent"},
		Simulation:      &Simulation{Model: "gpt-4o-mini", NumConversations: 1, MaxTurns: 5},
	}
}

// Spec §4: the combinations a simulation eval must and must not have.
//
// Checked here rather than at the run door so that deploying a contradiction
// and running one are refused alike. A run is billed whether or not the
// declaration made sense, and `azd up` accepting what `eval run` rejects is how
// the contradiction reached the point of being billed.
func TestValidateRunnable_SimulationCombinationRules(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Eval)
		wantErr string
	}{
		{
			name:    "turn level cannot produce conversations",
			mutate:  func(e *Eval) { e.EvaluationLevel = EvaluationLevelTurn },
			wantErr: `evaluation_level is "turn"`,
		},
		{
			name:    "an unstated level is named as unset rather than blank",
			mutate:  func(e *Eval) { e.EvaluationLevel = "" },
			wantErr: `evaluation_level is "unset"`,
		},
		{
			// With a dataset as well, the older rule reaches it first and says
			// the same thing in more general terms. Both refuse; neither runs
			// one origin and ignores the other.
			name:    "seeds and a source are two origins for the same rows",
			mutate:  func(e *Eval) { e.Source = &SourceDecl{Type: SourceTypeTraces, AgentName: "a"} },
			wantErr: "both say where rows come from",
		},
		{
			name: "a simulation that reads a source has nothing to simulate from",
			mutate: func(e *Eval) {
				e.Dataset = ""
				e.Source = &SourceDecl{Type: SourceTypeTraces, AgentName: "a"}
			},
			wantErr: "describe different runs",
		},
		{
			name:    "a conversation needs someone to talk to",
			mutate:  func(e *Eval) { e.Target = nil },
			wantErr: "target is required for a simulation",
		},
		{
			// The general target rule reaches this first and says the more
			// specific thing: the target is there, its name is not.
			name:    "a target with no name is reported as the missing name",
			mutate:  func(e *Eval) { e.Target = &Target{Type: TargetTypeAgent} },
			wantErr: "target.name is required",
		},
		{
			name:    "a model is not an agent",
			mutate:  func(e *Eval) { e.Target.Type = TargetTypeModel },
			wantErr: "target.type is model",
		},
		{
			name:    "seeds have to come from somewhere",
			mutate:  func(e *Eval) { e.Dataset = "" },
			wantErr: "dataset is required for a simulation",
		},
		{
			name:    "the simulated user needs a model",
			mutate:  func(e *Eval) { e.Simulation.Model = "" },
			wantErr: "simulation.model is required",
		},
		{
			// Refused rather than ignored: the run references the whole
			// registered seed dataset, so a cap that was accepted here would
			// report a bounded run and create a conversation per seed anyway.
			name:    "a cap cannot be applied to a referenced seed dataset",
			mutate:  func(e *Eval) { e.MaxSamples = 5 },
			wantErr: "max_samples is 5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eval := runnableSimulation()
			tc.mutate(&eval)

			err := ValidateRunnable(&eval)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// Spec §13, acceptance tests 7 and 8: the bounds accept their ends and reject
// one past them.
func TestValidateRunnable_SimulationNumericBounds(t *testing.T) {
	cases := []struct {
		name          string
		conversations int
		maxTurns      int
		wantErr       string
	}{
		{name: "one conversation, one turn", conversations: 1, maxTurns: 1},
		{name: "five conversations, twenty turns", conversations: 5, maxTurns: 20},
		{name: "no conversations", conversations: -1, wantErr: "num_conversations is -1"},
		{name: "six conversations", conversations: 6, wantErr: "num_conversations is 6"},
		{name: "zero turns is one below the floor", maxTurns: -1, wantErr: "max_turns is -1"},
		{name: "twenty-one turns", maxTurns: 21, wantErr: "max_turns is 21"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eval := runnableSimulation()
			eval.Simulation.NumConversations = tc.conversations
			eval.Simulation.MaxTurns = tc.maxTurns

			err := ValidateRunnable(&eval)

			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// The rules are reached through the configuration door as well, which is the
// half that was missing: deploying an eval and running it are two entries to
// the same declaration, and only one of them used to apply these.
func TestEvalConfigValidate_AppliesSimulationRules(t *testing.T) {
	eval := runnableSimulation()
	eval.EvaluationLevel = EvaluationLevelTurn
	eval.Evaluators = evalcore.EvaluatorList{{Evaluator: "builtin.relevance"}}

	cfg := &EvalConfig{
		Datasets: []DatasetDecl{{Name: "d", File: "./datasets/seeds.jsonl"}},
		Evals:    []Eval{eval},
	}

	err := cfg.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a simulation produces conversations")
	assert.Contains(t, err.Error(), "retail-multiturn",
		"the configuration door has an index and a name to report")
}
