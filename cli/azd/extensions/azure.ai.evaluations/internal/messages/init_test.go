// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitHandoffSelectsSimulationOnlyForGeneratedConversationSeeds(t *testing.T) {
	conversation := InitHandoffCommand("agent", "seeds", "conversation", "rubric")
	assert.Contains(t, conversation, "--conversation-mode simulation")
	assert.Contains(t, conversation, "--evaluation-level conversation")
	assert.Contains(t, conversation, "--evaluator builtin.task_completion --evaluator rubric")
	assert.NotContains(t, conversation, "--simulation-model")
	assert.NotContains(t, conversation, "--judge-model")
	assert.NotContains(t, conversation, "--no-prompt")
	for _, cmd := range []string{
		InitHandoffCommand("agent", "turns", "turn", "rubric"),
		InitHandoffCommand("agent", "", "", "rubric"),
	} {
		assert.NotContains(t, cmd, "--conversation-mode")
	}
}

func TestInitHandoffQuotesEveryArgument(t *testing.T) {
	cmd := InitHandoffCommand("agent name", "seed rows", "conversation", "rubric name")
	assert.Contains(t, cmd, "--target "+ShellArg("agent name"))
	assert.Contains(t, cmd, "--dataset "+ShellArg("seed rows"))
	assert.Contains(t, cmd, "--evaluator "+ShellArg("rubric name"))
	unsafe := InitHandoffCommand("agent", "seeds", "turn; unexpected", "")
	assert.Contains(t, unsafe, `--evaluation-level "turn; unexpected"`)
}
