// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package failuretext

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTextIsOneRedactedBoundedLine(t *testing.T) {
	got := Text("could not read https://user:pw@storage.example/rows.jsonl?sig=secret\nsecond   line " +
		strings.Repeat("x", 600))
	for _, secret := range []string{"user:pw", "secret", "sig="} {
		assert.NotContains(t, got, secret)
	}
	assert.NotContains(t, got, "\n")
	assert.Contains(t, got, "second line")
	assert.True(t, strings.HasSuffix(got, "..."))
	assert.LessOrEqual(t, len([]rune(got)), MaxRunes+3)
	assert.Equal(t, "plain", Text("  plain  "))
}

func TestLinesDropRestatementsAndRepeatsKeepTargetsAndCountTheRest(t *testing.T) {
	lines, more := Lines("Evaluation validation failed.", []Detail{
		{Message: "evaluation validation failed"},     // inside the headline
		{Message: "gpt-4o-mini"}, {Message: "gpt-4o"}, // not repeats of each other
		{Message: "gpt-4o-mini"},                            // an exact repeat
		{Message: "validation failed", Target: "run.model"}, // inside the headline, but names a target
		{Code: "OnlyACode"},
		{},
		{Message: "d5"}, {Message: "d6"},
	}, 4)
	assert.Equal(t, []string{"gpt-4o-mini", "gpt-4o", "validation failed (target: run.model)", "OnlyACode"}, lines)
	assert.Equal(t, 2, more)
}
