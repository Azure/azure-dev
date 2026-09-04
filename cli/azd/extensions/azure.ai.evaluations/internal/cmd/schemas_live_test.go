// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cmd

import (
	"context"
	"testing"

	"azureaieval/internal/pkg/eval_api"
	"azureaieval/internal/pkg/evalcore"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveEvaluatorSchemasIncludesBuiltins covers the function that supplies
// the schemas, rather than the builder that consumes them.
//
// The builder was already tested against every built-in, but the test fetched
// them itself with the Builtin filter. Production did not: it listed
// unfiltered, which returns only the project's own evaluators, so every
// built-in reached the builder with no schema at all. The builder was correct
// and the criteria were still wrong, and no test could see it because each one
// constructed the input production was failing to construct.
func TestLiveEvaluatorSchemasIncludesBuiltins(t *testing.T) {
	client, _ := liveEvalClient(t)
	ctx := context.Background()

	// The listing production used to rely on, to show what it omits.
	unfiltered, err := client.ListEvaluators(ctx, "", ProjectEndpointAPIVersion)
	require.NoError(t, err)
	builtinsInUnfiltered := 0
	for _, e := range unfiltered.Value {
		if eval_api.IsBuiltinEvaluator(e.Name) {
			builtinsInUnfiltered++
		}
	}

	ec := &evalContext{evalClient: client}
	schemas := ec.evaluatorSchemas(ctx)
	require.NotEmpty(t, schemas, "no evaluator schemas were resolved at all")

	builtins := 0
	for name, summary := range schemas {
		if !eval_api.IsBuiltinEvaluator(name) {
			continue
		}
		builtins++
		assert.NotNil(t, summary.DataSchema(),
			"%s resolved without the contract the criteria are shaped from", name)
	}

	require.NotZero(t, builtins,
		"built-ins must be resolvable; the unfiltered listing returns %d of them, "+
			"so they have to be asked for by type", builtinsInUnfiltered)
}

// The fields an evaluator declares are the ones its criterion has to bind, so
// a conversation-level evaluator must resolve to its conversation field.
func TestLiveConversationEvaluatorBindsMessages(t *testing.T) {
	client, judge := liveEvalClient(t)
	ctx := context.Background()

	ec := &evalContext{evalClient: client}
	schemas := ec.evaluatorSchemas(ctx)
	require.NotEmpty(t, schemas)

	var name string
	for n, summary := range schemas {
		if eval_api.IsBuiltinEvaluator(n) && summary.SupportsLevel("conversation") {
			if ds := summary.DataSchema(); ds != nil && ds.Accepts(conversationField) {
				name = n
				break
			}
		}
	}
	if name == "" {
		t.Skip("no built-in advertises a conversation contract on this project")
	}

	plan, err := planCriterion(
		evalcore.EvaluatorRef{
			Name:                     name,
			InitializationParameters: map[string]any{"deployment_name": judge},
		},
		schemas[name],
		nil, // no target: the dataset holds both sides of the exchange
		map[string]bool{conversationField: true},
		"conversation",
	)
	require.NoError(t, err)
	assert.Equal(t, "{{item."+conversationField+"}}", plan.dataMapping[conversationField],
		"%s must bind its conversation field", name)
	assert.NotEmpty(t, plan.dataMapping, "an empty mapping scores nothing")
}
