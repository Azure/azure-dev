// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An eval is immutable, so editing a declaration creates another under the same
// name and leaves the previous one holding its run history. Deleting takes the
// runs with it, so a name that matches several has to be refused rather than
// resolved to whichever happens to sort first.
func TestAmbiguousEvalName(t *testing.T) {
	got := AmbiguousEvalName("support-agent-eval", []string{"eval_aaa", "eval_bbb"}).Error()

	assert.Contains(t, got, `2 evals are named "support-agent-eval"`)
	assert.Contains(t, got, "eval_aaa")
	assert.Contains(t, got, "eval_bbb")
	assert.Contains(t, got, "discards its runs", "the reason it will not guess is the cost of guessing")
}
