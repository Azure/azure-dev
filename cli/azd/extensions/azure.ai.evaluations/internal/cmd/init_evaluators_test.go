// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"testing"

	"azureaieval/internal/pkg/evalcore"
	"azureaieval/internal/project"

	"github.com/stretchr/testify/assert"
)

// What an eval grades on is a SET, so there is no "the only one" to detect the
// way there is for the target and the judge model. Which criteria define
// quality is the substantive decision in the configuration, so init asks.
//
// The default is one built-in. It used to add a rubric generated from the
// agent's instructions, which meant init declared an evaluator file nothing had
// produced yet and `azd up` failed on it.
func TestDefaultEvaluatorsProposeOnlyWhatAlreadyResolves(t *testing.T) {
	assert.Equal(t,
		[]string{evalcore.BuiltinPrefix + "task_adherence"},
		defaultEvaluators())
}

// The prompt offers what is knowable without a service call -- init makes none
// -- which is the built-in it proposes plus whatever the catalog already
// declares. A declaration is offered because its file already exists; nothing
// that would have to be generated first appears here.
func TestEvaluatorChoicesOfferTheCatalogToo(t *testing.T) {
	cfg := &project.EvalConfig{Evaluators: []project.EvaluatorDecl{
		{Name: "support-agent-quality"},
		{Name: "tone-check"},
	}}

	got := evaluatorChoices(cfg)

	assert.Equal(t, []string{
		evalcore.BuiltinPrefix + "task_adherence",
		"support-agent-quality",
		"tone-check",
	}, got)
}
