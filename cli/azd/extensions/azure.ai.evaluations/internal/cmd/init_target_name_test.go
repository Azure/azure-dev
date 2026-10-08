// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --target names the azure.yaml service, which is a local label. The eval's
// target has to be the name the agent is published under, because that is what
// the run API invokes.
//
// Writing the key produced a config that read correctly, deployed cleanly, and
// ran fifteen test cases against a different agent -- or none. Nothing in the
// output said so, because every line of it uses the local name.
func TestTheEvalTargetIsThePublishedAgentName(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName:     "smoke",
		target:       "support-agent",
		remoteTarget: "hero-agent",
		dataset:      "prod-golden",
		evaluators:   []string{"builtin.task_adherence"},
		judgeModel:   "m",
	})

	require.NotNil(t, plan.eval.Target)
	assert.Equal(t, "hero-agent", plan.eval.Target.Name,
		"the run API invokes this name, so it has to be the one the service knows")
	assert.Equal(t, project.TargetTypeAgent, plan.eval.Target.Type)
}

// The description is what a reader recognizes, and they recognize the name they
// typed. Only the target the API acts on has to be translated.
func TestTheDescriptionKeepsTheNameTheAuthorTyped(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName:     "smoke",
		target:       "support-agent",
		remoteTarget: "hero-agent",
		dataset:      "prod-golden",
		evaluators:   []string{"builtin.task_adherence"},
		judgeModel:   "m",
	})

	assert.Contains(t, plan.eval.Description, "support-agent")
}

// Outside a project, or for an agent no local service declares, there is
// nothing to translate and the target stands as written. Falling back to empty
// would write an eval with no target at all.
func TestAnUnresolvedTargetStandsAsWritten(t *testing.T) {
	plan, _ := scaffoldFor(t, scaffoldInput{
		evalName:   "smoke",
		target:     "support-agent",
		dataset:    "prod-golden",
		evaluators: []string{"builtin.task_adherence"},
		judgeModel: "m",
	})

	require.NotNil(t, plan.eval.Target)
	assert.Equal(t, "support-agent", plan.eval.Target.Name)
}
