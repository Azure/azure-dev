// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kindsOf reduces the built sources to what --from talks about, which is the
// only part these tests are asserting on.
func kindsOf(sources []GenerationSource) []string {
	kinds := make([]string, 0, len(sources))
	for _, s := range sources {
		kinds = append(kinds, s.Type)
	}
	return kinds
}

// Naming a source is a request to send that one, not a hint. Everything the
// plan could otherwise have offered stays out of the request.
//
// Demonstrated with prompt because it stands alone. Agent and traces are both
// refused by the service unless a prompt or an agent accompanies them, so each
// carries one; that carve-out is pinned in their own tests.
func TestBuildGenerationSources_SendsOnlyWhatFromNamed(t *testing.T) {
	sources, unbuildable := BuildGenerationSources(
		[]string{"prompt"},
		"support-agent", "3", "answer support questions",
		&TraceOptions{Days: 7},
	)

	assert.Equal(t, []string{"prompt"}, kindsOf(sources))
	assert.Empty(t, unbuildable)
}

// Generating from an agent means generating from its instructions, so asking
// for the agent carries them. It is also the only shape the service honours:
// the agent source on its own fails for every agent, so a `--from agent` that
// dropped the prompt would be a request that always fails.
func TestBuildGenerationSources_AgentCarriesItsInstructions(t *testing.T) {
	sources, unbuildable := BuildGenerationSources(
		[]string{"agent"}, "support-agent", "3", "answer support questions", nil,
	)

	assert.Equal(t, []string{"prompt", "agent"}, kindsOf(sources))
	assert.Equal(t, "answer support questions", sources[0].Prompt)
	assert.Empty(t, unbuildable)
}

// The instructions ride along with the agent; they do not stand in for it. An
// agent nobody named is still nothing to generate from.
func TestBuildGenerationSources_InstructionsDoNotSubstituteForTheAgent(t *testing.T) {
	sources, unbuildable := BuildGenerationSources(
		[]string{"agent"}, "", "", "answer support questions", nil,
	)

	assert.Empty(t, sources)
	assert.Equal(t, []string{"agent"}, unbuildable)
}

// The agent name travels with the traces source: it is what scopes the query
// to this agent's conversations rather than the whole project's.
//
// The agent and its instructions are also sent as sources of their own, because
// the service refuses a request carrying neither a prompt nor an agent.
// Verified against it: traces alone is a 400 naming that requirement, traces
// plus agent is accepted. The prompt is what the agent-seeding retry falls back
// to, so without it a traces run has no way through that failure.
func TestBuildGenerationSources_TracesCarryTheAgent(t *testing.T) {
	sources, _ := BuildGenerationSources(
		[]string{"traces"}, "support-agent", "", "answer support questions",
		&TraceOptions{Days: 7},
	)

	assert.Equal(t, []string{"prompt", "agent", "traces"}, kindsOf(sources))
	assert.True(t, HasPromptSource(WithoutAgentSource(sources)),
		"dropping the agent must leave something the service still accepts")
}

// Without an agent the traces source names nothing to read, and nothing the
// service accepts can accompany it, so it is refused here rather than sent.
func TestBuildGenerationSources_TracesNeedAnAgent(t *testing.T) {
	sources, unbuildable := BuildGenerationSources(
		[]string{"traces"}, "", "", "", nil,
	)

	assert.Empty(t, sources)
	assert.Equal(t, []string{"traces"}, unbuildable)
}

// A day window narrows the trace query; it is not what authorizes it. The
// documented `dataset generate <name> --from traces` carries no window, and it
// has to mean "every trace" rather than "no traces".
func TestBuildGenerationSources_TracesWithoutAWindowAreUnbounded(t *testing.T) {
	sources, unbuildable := BuildGenerationSources(
		[]string{"traces"}, "support-agent", "", "", nil,
	)

	require.Len(t, sources, 2)
	assert.Equal(t, "traces", sources[1].Type)
	assert.Zero(t, sources[1].StartTime,
		"an absent window must leave start_time off the wire, not pin it to now")
	assert.Empty(t, unbuildable)
}

func TestBuildGenerationSources_TraceWindowBecomesAStartTime(t *testing.T) {
	sources, _ := BuildGenerationSources(
		[]string{"traces"}, "support-agent", "", "", &TraceOptions{Days: 7},
	)

	require.Len(t, sources, 2)
	want := time.Now().AddDate(0, 0, -7).Unix()
	assert.InDelta(t, want, sources[1].StartTime, 60)
}

// No --from is no preference, so the plan sends everything it happens to have.
func TestBuildGenerationSources_EmptyFromSendsWhatThePlanHas(t *testing.T) {
	sources, unbuildable := BuildGenerationSources(
		nil, "support-agent", "3", "answer support questions", &TraceOptions{Days: 7},
	)

	assert.Equal(t, []string{"prompt", "agent", "traces"}, kindsOf(sources))
	assert.Empty(t, unbuildable)
}

// Expressing no preference cannot disappoint one, so an empty --from reports
// nothing missing however little the plan turns out to hold.
func TestBuildGenerationSources_EmptyFromNeverReportsMissingSources(t *testing.T) {
	sources, unbuildable := BuildGenerationSources(nil, "", "", "", nil)

	assert.Empty(t, sources)
	assert.Empty(t, unbuildable)
}

// Asking for a source the plan cannot build has to surface, because the job is
// billed and what comes back looks the same either way.
func TestBuildGenerationSources_ReportsWhatItCouldNotBuild(t *testing.T) {
	tests := []struct {
		name        string
		kinds       []string
		agentName   string
		instruction string
		want        []string
	}{
		{
			name:  "prompt without an instruction",
			kinds: []string{"prompt"},
			want:  []string{"prompt"},
		},
		{
			name:  "agent without a target",
			kinds: []string{"agent"},
			want:  []string{"agent"},
		},
		{
			name:  "file is not a generation source at all",
			kinds: []string{"file"},
			want:  []string{"file"},
		},
		{
			name:  "several at once",
			kinds: []string{"prompt", "agent"},
			want:  []string{"agent", "prompt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources, unbuildable := BuildGenerationSources(
				tt.kinds, tt.agentName, "", tt.instruction, nil,
			)

			assert.Empty(t, sources)
			assert.Equal(t, tt.want, unbuildable)
		})
	}
}

// A request that names two sources and can only build one still reports the
// one it could not, rather than being satisfied by the other's success.
func TestBuildGenerationSources_OneBuiltSourceDoesNotExcuseAMissingOne(t *testing.T) {
	sources, unbuildable := BuildGenerationSources(
		[]string{"agent", "prompt"}, "support-agent", "", "", nil,
	)

	assert.Equal(t, []string{"agent"}, kindsOf(sources))
	assert.Equal(t, []string{"prompt"}, unbuildable)
}

// `file` is only unbuildable when it was asked for. The default sweep must not
// invent a complaint about a source nobody named.
func TestBuildGenerationSources_FileIsOnlyReportedWhenAskedFor(t *testing.T) {
	_, unbuildable := BuildGenerationSources(
		nil, "support-agent", "", "instruction", &TraceOptions{Days: 7},
	)

	assert.Empty(t, unbuildable)
}

func TestBuildGenerationSources_AgentVersionIsOptional(t *testing.T) {
	withVersion, _ := BuildGenerationSources([]string{"agent"}, "support-agent", "3", "", nil)
	require.Len(t, withVersion, 1)
	assert.Equal(t, "3", withVersion[0].AgentVersion)

	withoutVersion, _ := BuildGenerationSources([]string{"agent"}, "support-agent", "", "", nil)
	require.Len(t, withoutVersion, 1)
	assert.Empty(t, withoutVersion[0].AgentVersion)
}

// The retry that saves the documented flow: agent-seeded generation fails
// server-side for every agent, and the same request without the agent source
// succeeds.
func TestWithoutAgentSource(t *testing.T) {
	sources := []GenerationSource{
		{Type: "prompt", Prompt: "be helpful"},
		{Type: "agent", AgentName: "support"},
		{Type: "traces", AgentName: "support"},
	}

	kept := WithoutAgentSource(sources)

	assert.Equal(t, []string{"prompt", "traces"}, kindsOf(kept))
	assert.Len(t, sources, 3, "the original must not be modified; it is retried from")
}

// The retry only happens when something is left to generate from, so this is
// what stops a second billed job that would fail the same way.
func TestHasPromptSource(t *testing.T) {
	assert.True(t, HasPromptSource([]GenerationSource{{Type: "prompt", Prompt: "x"}}))
	assert.False(t, HasPromptSource([]GenerationSource{{Type: "prompt"}}),
		"an empty prompt is nothing to generate from")
	assert.False(t, HasPromptSource([]GenerationSource{{Type: "agent", AgentName: "s"}}))
	assert.False(t, HasPromptSource(nil))
}

// The request body is what the service validates, so the fields it keys on are
// pinned rather than left to whatever the builder happens to set.
func TestNewDataGenerationJobRequest(t *testing.T) {
	sources := []GenerationSource{{Type: "prompt", Prompt: "be helpful"}}

	req := NewDataGenerationJobRequest("support-regression", "gpt-4o", 15, sources)

	require.NotNil(t, req)
	assert.Equal(t, "support-regression", req.Inputs.Name)
	assert.Equal(t, "evaluation", req.Inputs.Scenario)
	assert.Equal(t, "simple_qna", req.Inputs.Options.Type)
	assert.Equal(t, 15, req.Inputs.Options.MaxSamples)
	assert.Equal(t, "gpt-4o", req.Inputs.Options.ModelOptions.Model)
	assert.Equal(t, sources, req.Inputs.Sources)
}

// The evaluator request sends the name twice, under two keys the service reads
// separately. Setting only one produces a job that runs and returns an
// evaluator under the wrong name.
func TestNewEvaluatorGenerationJobRequest(t *testing.T) {
	sources := []GenerationSource{{Type: "prompt", Prompt: "grade politeness"}}

	req := NewEvaluatorGenerationJobRequest("support-quality", "gpt-4o", sources)

	require.NotNil(t, req)
	assert.Equal(t, "support-quality", req.Inputs.Name)
	assert.Equal(t, "support-quality", req.Inputs.EvaluatorName)
	assert.Equal(t, "gpt-4o", req.Inputs.Model)
	assert.Equal(t, sources, req.Inputs.Sources)
}
