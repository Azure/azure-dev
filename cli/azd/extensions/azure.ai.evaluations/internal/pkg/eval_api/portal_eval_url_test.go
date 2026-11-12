// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProjectResourceID = "/subscriptions/00000000-0000-0000-0000-000000000000" +
	"/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj"

// `eval create` reported the id and stopped, so the one thing a reader wanted
// next -- somewhere to look at it -- was missing. ADO 5571804.
func TestEvalURL_PointsAtTheEval(t *testing.T) {
	t.Parallel()

	prefix, err := NewPortalPrefix(testProjectResourceID)
	require.NoError(t, err)

	url := prefix.EvalURL("eval_4672f4a623d345ec9d2d640cc4af40c8")

	assert.True(t, strings.HasPrefix(url, "https://ai.azure.com/"), "url was %q", url)
	assert.Contains(t, url, "/build/evaluations/eval_4672f4a623d345ec9d2d640cc4af40c8")
	assert.NotContains(t, url, "/run/", "an eval is not a run")
}

// The run URL is this URL plus a run, so the two must agree on where an eval
// lives. A reader following one and then the other should not land in two
// different places.
func TestEvalURL_IsThePrefixOfTheRunURL(t *testing.T) {
	t.Parallel()

	prefix, err := NewPortalPrefix(testProjectResourceID)
	require.NoError(t, err)

	assert.True(t,
		strings.HasPrefix(prefix.EvalRunURL("eval_1", "run_1"), prefix.EvalURL("eval_1")),
		"the run URL should extend the eval URL")
}

// Ids are the service's, not this extension's, so a character that means
// something in a URL has to be escaped rather than interpolated.
func TestEvalURL_EscapesTheID(t *testing.T) {
	t.Parallel()

	prefix, err := NewPortalPrefix(testProjectResourceID)
	require.NoError(t, err)

	url := prefix.EvalURL("weird id/with slash")

	assert.NotContains(t, url, " ", "a space would break the link when pasted")
	assert.NotContains(t, url, "id/with", "an unescaped slash points somewhere else entirely")
}
