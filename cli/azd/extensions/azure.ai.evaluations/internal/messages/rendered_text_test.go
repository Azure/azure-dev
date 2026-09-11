// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// JudgeModelRequired shipped ending at "to read one from" -- the half of the
// sentence naming the way out never rendered. It is the first error a new user
// hits, and nothing caught it because no test asserted the rendered string:
// checking the error type or a prefix cannot see a missing tail.
func TestJudgeModelRequiredNamesBothPlacesItLooked(t *testing.T) {
	msg := JudgeModelRequired().Error()

	assert.Contains(t, msg, "--judge-model", "the flag is the whole of the way out")
	assert.Contains(t, msg, "AZURE_AI_MODEL_DEPLOYMENT_NAME",
		"the other place a deployment is read from has to be named")
	assert.NotContains(t, msg, "to read one from", "the sentence must not stop mid-clause")
	assert.False(t, strings.HasSuffix(strings.TrimRight(msg, " "), ":"),
		"a trailing colon promises a clause that never arrives: %q", msg)
}
