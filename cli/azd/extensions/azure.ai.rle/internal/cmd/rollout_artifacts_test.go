// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// realGymRolloutGraph is the shape the service actually returns, trimmed to three tokens
// per array. Taken from a live math_rl 1.0.6 rollout so the reshaping is tested against
// the real field names rather than an invented contract.
const realGymRolloutGraph = `{
  "metadata": {"api_type": "openai_chat"},
  "turns": [
    {"node_id": "d3078af988d4", "index": 0, "root_id": "d3078af988d4", "n_sampled": 2894,
     "n_prompt": 137, "n_tools": 0, "finish_reason": "stop",
     "sampling_params": {"temperature": 1, "top_p": 1}, "discarded": false}
  ],
  "stats": {"n_turns": 1, "n_roots": 1, "n_leaves": 1, "n_forks": 0, "n_discarded": 0,
            "n_sequences": 1, "n_trainable_sequences": 1, "n_trainable_tokens": 2894},
  "sequences": [
    {"role": "agent", "root_id": "d3078af988d4", "node_ids": ["d3078af988d4"], "n_turns": 1,
     "prompt_len": 137, "n_trainable": 2894, "turn_lengths": [2894],
     "input_ids": [151644, 872, 198], "loss_mask": [0, 0, 1], "logprobs": [0.0, 0.0, -0.5],
     "trainable": true, "validation": ["[INFO] certain_tokens"]}
  ],
  "validation": ["[INFO] single_turn_episode"],
  "trainable": true,
  "capture_level": "tokens",
  "rollout_type": "train"
}`

func testGymResponse() *executeRolloutResponse {
	return &executeRolloutResponse{
		RolloutID: "4f53e172018b9d7f74825dc348e42386",
		Rollout:   json.RawMessage(realGymRolloutGraph),
		Reward:    1,
		Success:   false,
		Result:    json.RawMessage(`{"correct":true,"format":true}`),
		Episode: &executeRolloutGymEpisode{
			Kind:              "gym_openenv",
			TerminationReason: "done",
			Steps: []executeRolloutGymStep{
				{CaptureNodeID: "d3078af988d4", Reward: 1, EpisodeDone: true},
			},
		},
	}
}

func TestWriteRolloutArtifactsWritesEveryFile(t *testing.T) {
	root := t.TempDir()
	artifacts, err := writeRolloutArtifacts(root, testGymResponse())
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]bool{
		"summary.json":     false,
		"turns.json":       false,
		"rollout.json":     false,
		"sequences/0.json": false,
	}
	for _, file := range artifacts.Files {
		if _, known := expected[file.Path]; !known {
			t.Fatalf("unexpected artifact %q", file.Path)
		}
		expected[file.Path] = true
		if file.Description == "" {
			t.Fatalf("artifact %q was written without a description", file.Path)
		}
		if _, err := os.Stat(filepath.Join(artifacts.Dir, filepath.FromSlash(file.Path))); err != nil {
			t.Fatalf("artifact %q was listed but not written: %v", file.Path, err)
		}
	}
	for path, written := range expected {
		if !written {
			t.Fatalf("expected %q to be written", path)
		}
	}

	if filepath.Base(artifacts.Dir) != "4f53e172018b9d7f74825dc348e42386" {
		t.Fatalf("expected the rollout id to name the directory, got %q", artifacts.Dir)
	}
}

// The verbatim graph is the only copy: the capture session is closed and deleted when the
// rollout returns, so a field this command does not model must still survive the round
// trip.
func TestWriteRolloutArtifactsPreservesTheGraphVerbatim(t *testing.T) {
	root := t.TempDir()
	artifacts, err := writeRolloutArtifacts(root, testGymResponse())
	if err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(filepath.Join(artifacts.Dir, "rollout.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(written, &got); err != nil {
		t.Fatalf("rollout.json is not valid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(realGymRolloutGraph), &want); err != nil {
		t.Fatal(err)
	}
	gotEncoded, _ := json.Marshal(got)
	wantEncoded, _ := json.Marshal(want)
	if string(gotEncoded) != string(wantEncoded) {
		t.Fatalf("rollout.json changed the graph:\n got %s\nwant %s", gotEncoded, wantEncoded)
	}
}

func TestWriteRolloutArtifactsSummaryIndexesSequencesWithoutTheirArrays(t *testing.T) {
	root := t.TempDir()
	artifacts, err := writeRolloutArtifacts(root, testGymResponse())
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(artifacts.Dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary rolloutSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}

	if summary.Reward != 1 || summary.Success {
		t.Fatalf("expected the outcome to be carried, got reward=%v success=%v", summary.Reward, summary.Success)
	}
	if summary.CaptureLevel != "tokens" || summary.RolloutType != "train" {
		t.Fatalf("expected the capture level to be carried, got %q/%q", summary.CaptureLevel, summary.RolloutType)
	}
	if len(summary.Sequences) != 1 {
		t.Fatalf("expected one indexed sequence, got %d", len(summary.Sequences))
	}
	sequence := summary.Sequences[0]
	if sequence.File != "sequences/0.json" {
		t.Fatalf("expected the summary to name the sequence file, got %q", sequence.File)
	}
	if sequence.NInputIDs != 3 || sequence.NTrainable != 2894 {
		t.Fatalf("expected the sequence shape to be summarised, got %#v", sequence)
	}
	// The arrays are what make the summary unreadable; only their lengths belong here.
	if strings.Contains(string(raw), "151644") {
		t.Fatal("summary.json must not inline the token arrays")
	}
}

func TestWriteRolloutArtifactsKeepsTheTokenArraysIntact(t *testing.T) {
	root := t.TempDir()
	artifacts, err := writeRolloutArtifacts(root, testGymResponse())
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(artifacts.Dir, "sequences", "0.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sequence capturedSequence
	if err := json.Unmarshal(raw, &sequence); err != nil {
		t.Fatal(err)
	}
	if len(sequence.InputIDs) != 3 || sequence.InputIDs[0] != 151644 {
		t.Fatalf("expected input_ids to survive, got %#v", sequence.InputIDs)
	}
	if len(sequence.LossMask) != 3 || len(sequence.Logprobs) != 3 {
		t.Fatalf("expected the aligned arrays to survive, got mask=%d logprobs=%d",
			len(sequence.LossMask), len(sequence.Logprobs))
	}
	if sequence.Logprobs[2] != -0.5 {
		t.Fatalf("expected logprob values to survive, got %#v", sequence.Logprobs)
	}
}

// A rollout whose graph is missing or malformed still has a reward worth keeping. The
// artifacts shrink; the command does not fail.
func TestWriteRolloutArtifactsToleratesAnUnusableGraph(t *testing.T) {
	for name, rollout := range map[string]json.RawMessage{
		"absent":       nil,
		"malformed":    json.RawMessage(`{"sequences": "not-a-list"}`),
		"empty object": json.RawMessage(`{}`),
	} {
		t.Run(name, func(t *testing.T) {
			response := testGymResponse()
			response.Rollout = rollout

			artifacts, err := writeRolloutArtifacts(t.TempDir(), response)
			if err != nil {
				t.Fatalf("expected a usable artifact set, got %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(artifacts.Dir, "summary.json"))
			if err != nil {
				t.Fatalf("expected summary.json regardless of the graph: %v", err)
			}
			var summary rolloutSummary
			if err := json.Unmarshal(raw, &summary); err != nil {
				t.Fatal(err)
			}
			if summary.Reward != 1 {
				t.Fatalf("expected the reward to survive, got %v", summary.Reward)
			}
		})
	}
}

// The rollout id names a directory, and --rollout-id is caller-supplied.
func TestWriteRolloutArtifactsRejectsARolloutIdThatIsNotOnePathSegment(t *testing.T) {
	for _, id := range []string{"", "..", ".", "a/b", "../escape", "/absolute"} {
		response := testGymResponse()
		response.RolloutID = id
		if _, err := writeRolloutArtifacts(t.TempDir(), response); err == nil {
			t.Fatalf("expected rollout id %q to be rejected", id)
		}
	}
}

func TestPrintRolloutArtifactsRendersATreeWithSizesAndPurposes(t *testing.T) {
	artifacts, err := writeRolloutArtifacts(t.TempDir(), testGymResponse())
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := printRolloutArtifacts(&output, artifacts); err != nil {
		t.Fatal(err)
	}
	rendered := output.String()

	for _, want := range []string{
		"Artifacts:",
		"summary.json",
		"start here",
		"turns.json",
		"one entry per model call",
		"rollout.json",
		"exactly as the service returned it",
		"sequences/",
		"the unit a trainer consumes",
		"0.json",
		"input_ids, loss_mask, logprobs",
		"agent, 1 turn(s), 3 tokens (2894 trainable)",
		"├── ",
		"└── ",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected the tree to contain %q, got:\n%s", want, rendered)
		}
	}

	// The sequence file is nested, so it must be rendered under the directory row rather
	// than as another top-level entry.
	sequencesAt := strings.Index(rendered, "sequences/")
	sequenceFileAt := strings.Index(rendered, "0.json")
	if sequencesAt < 0 || sequenceFileAt < sequencesAt {
		t.Fatalf("expected sequences/0.json to render under sequences/, got:\n%s", rendered)
	}
}

// An eval rollout's arrays are empty by construction, which reads identically to a broken
// capture unless the description says which it is.
func TestPrintRolloutArtifactsDistinguishesAnEvalCapture(t *testing.T) {
	response := testGymResponse()
	response.Rollout = json.RawMessage(`{
	  "sequences": [{"role": "agent", "n_turns": 2, "prompt_len": 10, "n_trainable": 0,
	                 "input_ids": [], "loss_mask": [], "logprobs": [], "trainable": false}],
	  "capture_level": "text", "rollout_type": "eval", "trainable": false
	}`)

	artifacts, err := writeRolloutArtifacts(t.TempDir(), response)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := printRolloutArtifacts(&output, artifacts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "no token arrays (eval capture)") {
		t.Fatalf("expected the eval capture to be named, got:\n%s", output.String())
	}
}

// A nested row's tree prefix is wider than a top-level one, so padding the name alone
// drifts the size and description columns by exactly that difference. This is the defect
// the first live run showed.
func TestRenderArtifactTreeAlignsEveryRow(t *testing.T) {
	artifacts, err := writeRolloutArtifacts(t.TempDir(), testGymResponse())
	if err != nil {
		t.Fatal(err)
	}

	lines := renderArtifactTree(artifacts)
	if len(lines) < 2 {
		t.Fatalf("expected a multi-row tree, got %#v", lines)
	}

	// Every row carries a description, and all of them are laid out in one column. Find
	// where each begins, counting runes because the tree prefixes are multi-byte.
	column := -1
	for _, line := range lines {
		index := -1
		for _, file := range artifacts.Files {
			if at := strings.Index(line, file.Description); at >= 0 {
				index = at
				break
			}
		}
		if index < 0 {
			if at := strings.Index(line, artifactDirectoryDescription("sequences")); at >= 0 {
				index = at
			}
		}
		if index < 0 {
			t.Fatalf("row %q carries no description", line)
		}

		runes := utf8.RuneCountInString(line[:index])
		if column == -1 {
			column = runes
			continue
		}
		if runes != column {
			t.Fatalf("row %q starts its description at column %d, expected %d:\n%s",
				line, runes, column, strings.Join(lines, "\n"))
		}
	}
}

func TestFormatBytes(t *testing.T) {
	for size, want := range map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1.0 KB",
		78 * 1024:  "78.0 KB",
		1536:       "1.5 KB",
		1050000:    "1.0 MB",
		1073741824: "1.0 GB",
	} {
		if got := formatBytes(size); got != want {
			t.Fatalf("formatBytes(%d) = %q, want %q", size, got, want)
		}
	}
}
