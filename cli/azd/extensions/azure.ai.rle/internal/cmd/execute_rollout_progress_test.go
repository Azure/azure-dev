// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"azure.ai.rle/internal/rollouts"

	"github.com/fatih/color"
)

func TestDecodeExecuteRolloutProgressValidatesContract(t *testing.T) {
	valid := []byte(`{
		"message":"Step complete.",
		"phase":"environment_step",
		"status":"completed",
		"sequence":2,
		"elapsed_ms":25,
		"partial_response":{
			"rollout_id":"abc123",
			"reward":0,
			"success":false,
			"episode":{"kind":"gym_openenv","steps":[]}
		}
	}`)
	progress, err := decodeExecuteRolloutProgress(valid, "abc123", 1)
	if err != nil {
		t.Fatal(err)
	}
	if progress.PartialResponse.Reward == nil || *progress.PartialResponse.Reward != 0 {
		t.Fatalf("expected a known zero reward, got %#v", progress.PartialResponse)
	}
	if progress.PartialResponse.Success == nil || *progress.PartialResponse.Success {
		t.Fatalf("expected a known false success value, got %#v", progress.PartialResponse)
	}

	tests := []struct {
		name     string
		payload  string
		previous int64
	}{
		{"missing message", `{"phase":"x","status":"started","sequence":1,"elapsed_ms":0}`, 0},
		{"non-increasing sequence", `{"message":"x","phase":"x","status":"started","sequence":2,"elapsed_ms":0}`, 2},
		{"negative elapsed", `{"message":"x","phase":"x","status":"started","sequence":1,"elapsed_ms":-1}`, 0},
		{
			"mismatched partial rollout",
			`{"message":"x","phase":"x","status":"started","sequence":1,"elapsed_ms":0,` +
				`"partial_response":{"rollout_id":"other"}}`,
			0,
		},
		{
			"partial episode missing steps",
			`{"message":"x","phase":"x","status":"started","sequence":1,"elapsed_ms":0,` +
				`"partial_response":{"rollout_id":"abc123","episode":{"kind":"gym_openenv"}}}`,
			0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeExecuteRolloutProgress(
				[]byte(test.payload),
				"abc123",
				test.previous,
			); err == nil {
				t.Fatal("expected invalid progress to be rejected")
			}
		})
	}
}

func TestExecuteRolloutProgressRendererShowsLifecycleAndPartialOutcome(t *testing.T) {
	previousNoColor := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previousNoColor })

	termination := "terminated"
	ungraded := true
	reward := 1.25
	var output bytes.Buffer
	renderer := newExecuteRolloutProgressRenderer(&output)
	if err := renderer.Render(executeRolloutProgress{
		Message:             "Rollout execution finished.\n",
		Phase:               "target_execute",
		Status:              "completed",
		Sequence:            18,
		ElapsedMilliseconds: 42000,
		PartialResponse: &executeRolloutPartialResponse{
			RolloutID: "abc123",
			Reward:    &reward,
			Episode: &executeRolloutPartialEpisode{
				Kind:              "gym_openenv",
				Steps:             make([]rollouts.Step, 2),
				TerminationReason: &termination,
				Ungraded:          &ungraded,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, expected := range []string{
		"[     42s] (✓) Done: Rollout execution finished.",
		"2 steps · reward 1.25 · termination terminated · ungraded",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected %q in %q", expected, got)
		}
	}
	if strings.Contains(got, "\n\n") {
		t.Fatalf("expected control characters in the message to be flattened, got %q", got)
	}
}

func TestExecuteRolloutProgressRendererDoesNotRepeatRunningStepOutcome(t *testing.T) {
	previousNoColor := color.NoColor
	color.NoColor = true
	t.Cleanup(func() { color.NoColor = previousNoColor })

	totalReward := 1.25
	var output bytes.Buffer
	renderer := newExecuteRolloutProgressRenderer(&output)
	if err := renderer.Render(executeRolloutProgress{
		Message:             "Step 2/5 finished; reward 1, total reward 1.25.",
		Phase:               "environment_step",
		Status:              "completed",
		Sequence:            18,
		ElapsedMilliseconds: 42000,
		PartialResponse: &executeRolloutPartialResponse{
			RolloutID: "abc123",
			Reward:    &totalReward,
			Episode: &executeRolloutPartialEpisode{
				Kind: "gym_openenv",
				Steps: []rollouts.Step{
					{CaptureNodeID: "node-1", Reward: 0.25},
					{CaptureNodeID: "node-2", Reward: 1},
				},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	if strings.Count(got, "\n") != 1 {
		t.Fatalf("expected only the service progress line while the episode is running, got %q", got)
	}
	for _, duplicate := range []string{"steps complete", "latest reward", "episode running"} {
		if strings.Contains(got, duplicate) {
			t.Fatalf("expected %q not to be repeated in %q", duplicate, got)
		}
	}
}
