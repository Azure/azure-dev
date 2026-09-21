// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// defaultRolloutOutputDir is where a rollout's artifacts land unless --output-dir says
// otherwise. Relative to the working directory, so consecutive rollouts accumulate under
// one folder keyed by rollout id.
const defaultRolloutOutputDir = ".output"

// rolloutArtifacts is the on-disk form of one Execute Rollout response.
//
// The service returns the whole Capture Proxy graph — token ids, logprobs, loss masks and
// per-turn metadata — but a terminal cannot show it: a single-turn math rollout is already
// ~78 KB, dominated by three parallel token-aligned arrays. Printing it is useless and
// discarding it loses the only copy, since the capture session is closed and deleted as
// soon as the rollout returns.
//
// So it is written out, split by how it gets read. `summary.json` is the part a human
// checks; `rollout.json` is the verbatim record to diff or replay; the token arrays go to
// one file per sequence, because that is the unit a trainer consumes and the unit whose
// size makes the rest unreadable.
type rolloutArtifacts struct {
	// Dir is the absolute directory the files were written to.
	Dir string
	// Files is every file written, relative to Dir, in write order.
	Files []rolloutArtifactFile
}

// rolloutArtifactFile is one written file and what it is for.
//
// The description is carried rather than looked up at print time because two of the four
// are only knowable here: a sequence's shape comes from the sequence itself, and "which
// of these do I feed a trainer" is the question the tree exists to answer.
type rolloutArtifactFile struct {
	// Path is relative to the artifact directory, slash-separated.
	Path string
	// Description is one crisp line: what it holds, and what reads it.
	Description string
}

// rolloutSummary is the small, human-readable half: the outcome and the shape of the
// capture, without the arrays.
type rolloutSummary struct {
	RolloutID string          `json:"rollout_id"`
	Reward    float64         `json:"reward"`
	Success   bool            `json:"success"`
	Result    json.RawMessage `json:"result,omitempty"`

	Episode *executeRolloutGymEpisode `json:"episode,omitempty"`

	// Copied off the graph so the outcome and the capture's own verdict on itself can be
	// read together. `validation` is where a rollout explains why it cannot be trained on.
	CaptureLevel string          `json:"capture_level,omitempty"`
	RolloutType  string          `json:"rollout_type,omitempty"`
	Trainable    *bool           `json:"trainable,omitempty"`
	Stats        json.RawMessage `json:"stats,omitempty"`
	Validation   json.RawMessage `json:"validation,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`

	// Sequences describes each captured sequence without reproducing its arrays, and
	// names the file that holds them.
	Sequences []rolloutSequenceSummary `json:"sequences,omitempty"`
}

// rolloutSequenceSummary is one sequence's shape plus a pointer to its full arrays.
type rolloutSequenceSummary struct {
	Index       int    `json:"index"`
	Role        string `json:"role,omitempty"`
	RootID      string `json:"root_id,omitempty"`
	NTurns      int    `json:"n_turns"`
	PromptLen   int    `json:"prompt_len"`
	NTrainable  int    `json:"n_trainable"`
	Trainable   bool   `json:"trainable"`
	NInputIDs   int    `json:"n_input_ids"`
	NLogprobs   int    `json:"n_logprobs"`
	TurnLengths []int  `json:"turn_lengths,omitempty"`
	File        string `json:"file"`
}

// capturedGraph is the subset of the Capture Proxy graph this command reads. Everything
// it does not name is still preserved verbatim in rollout.json.
type capturedGraph struct {
	Metadata   json.RawMessage    `json:"metadata,omitempty"`
	Turns      json.RawMessage    `json:"turns,omitempty"`
	Stats      json.RawMessage    `json:"stats,omitempty"`
	Sequences  []capturedSequence `json:"sequences,omitempty"`
	Validation json.RawMessage    `json:"validation,omitempty"`
	Trainable  *bool              `json:"trainable,omitempty"`

	CaptureLevel string `json:"capture_level,omitempty"`
	RolloutType  string `json:"rollout_type,omitempty"`
}

// capturedSequence is one root-to-leaf path: the token-aligned arrays a trainer consumes,
// plus the metadata that says what they are.
type capturedSequence struct {
	Role        string          `json:"role,omitempty"`
	RootID      string          `json:"root_id,omitempty"`
	NodeIDs     []string        `json:"node_ids,omitempty"`
	NTurns      int             `json:"n_turns"`
	PromptLen   int             `json:"prompt_len"`
	NTrainable  int             `json:"n_trainable"`
	TurnLengths []int           `json:"turn_lengths,omitempty"`
	InputIDs    []int           `json:"input_ids,omitempty"`
	LossMask    []int           `json:"loss_mask,omitempty"`
	Logprobs    []float64       `json:"logprobs,omitempty"`
	Trainable   bool            `json:"trainable"`
	Validation  json.RawMessage `json:"validation,omitempty"`
}

// writeRolloutArtifacts writes one rollout's response under outputDir/<rollout id>/.
//
// A malformed or absent graph is not fatal. The response still carries the reward and the
// result, and losing those to a parse error on a field this function only reshapes would
// be a worse outcome than an artifact set with fewer files in it.
func writeRolloutArtifacts(
	outputDir string,
	response *executeRolloutResponse,
) (*rolloutArtifacts, error) {
	if response == nil {
		return nil, fmt.Errorf("no rollout response to write")
	}

	// The rollout id names the directory, so it has to be safe as a single path segment.
	// The service echoes back the caller-generated id, and this command generates a hex
	// GUID, but a caller may pass --rollout-id by hand.
	id := strings.TrimSpace(response.RolloutID)
	if id == "" || id != filepath.Base(id) || id == "." || id == ".." {
		return nil, fmt.Errorf("rollout id %q cannot be used as a folder name", response.RolloutID)
	}

	root, err := filepath.Abs(filepath.Join(outputDir, id))
	if err != nil {
		return nil, fmt.Errorf("resolve rollout output directory: %w", err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("create rollout output directory: %w", err)
	}

	artifacts := &rolloutArtifacts{Dir: root}

	var graph capturedGraph
	if len(response.Rollout) > 0 {
		// A graph that does not parse is still written verbatim below; only the derived
		// views are skipped.
		_ = json.Unmarshal(response.Rollout, &graph)
	}

	summary := rolloutSummary{
		RolloutID:    response.RolloutID,
		Reward:       response.Reward,
		Success:      response.Success,
		Result:       response.Result,
		Episode:      response.Episode,
		CaptureLevel: graph.CaptureLevel,
		RolloutType:  graph.RolloutType,
		Trainable:    graph.Trainable,
		Stats:        graph.Stats,
		Validation:   graph.Validation,
		Metadata:     graph.Metadata,
	}

	for i, sequence := range graph.Sequences {
		relative := filepath.Join("sequences", fmt.Sprintf("%d.json", i))
		summary.Sequences = append(summary.Sequences, rolloutSequenceSummary{
			Index:       i,
			Role:        sequence.Role,
			RootID:      sequence.RootID,
			NTurns:      sequence.NTurns,
			PromptLen:   sequence.PromptLen,
			NTrainable:  sequence.NTrainable,
			Trainable:   sequence.Trainable,
			NInputIDs:   len(sequence.InputIDs),
			NLogprobs:   len(sequence.Logprobs),
			TurnLengths: sequence.TurnLengths,
			File:        filepath.ToSlash(relative),
		})
		if err := writeJSONFile(artifacts, root, relative, describeSequence(sequence), sequence); err != nil {
			return nil, err
		}
	}

	if err := writeJSONFile(
		artifacts,
		root,
		"summary.json",
		"outcome, capture stats and the sequence index — start here",
		summary,
	); err != nil {
		return nil, err
	}
	if len(graph.Turns) > 0 {
		if err := writeRawFile(
			artifacts,
			root,
			"turns.json",
			"one entry per model call: token counts, finish reason, sampling params",
			graph.Turns,
		); err != nil {
			return nil, err
		}
	}
	if len(response.Rollout) > 0 {
		if err := writeRawFile(
			artifacts,
			root,
			"rollout.json",
			"the full capture graph exactly as the service returned it",
			response.Rollout,
		); err != nil {
			return nil, err
		}
	}

	return artifacts, nil
}

// describeSequence says what one sequence file is in the terms a trainer decides on:
// whether it is trainable at all, and how much of it carries loss.
func describeSequence(sequence capturedSequence) string {
	role := sequence.Role
	if role == "" {
		role = "sequence"
	}
	if len(sequence.InputIDs) == 0 {
		// An eval rollout. The path and its structure are real; the token arrays are
		// empty by construction, not by accident, so say which of the two it is.
		return fmt.Sprintf("%s, %d turn(s) — no token arrays (eval capture)", role, sequence.NTurns)
	}
	trainable := "not trainable"
	if sequence.Trainable {
		trainable = fmt.Sprintf("%d trainable", sequence.NTrainable)
	}
	return fmt.Sprintf(
		"%s, %d turn(s), %d tokens (%s) — input_ids, loss_mask, logprobs",
		role,
		sequence.NTurns,
		len(sequence.InputIDs),
		trainable,
	)
}

func writeJSONFile(
	artifacts *rolloutArtifacts,
	root string,
	relative string,
	description string,
	value any,
) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", relative, err)
	}
	return writeRawFile(artifacts, root, relative, description, encoded)
}

func writeRawFile(
	artifacts *rolloutArtifacts,
	root string,
	relative string,
	description string,
	content []byte,
) error {
	path := filepath.Join(root, relative)
	if directory := filepath.Dir(path); directory != root {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(relative), err)
		}
	}
	// Indented so the file can be read and diffed directly. The service emits compact
	// JSON, which for a token array is a single unreadable line.
	var indented strings.Builder
	if err := indentJSON(&indented, content); err == nil {
		content = []byte(indented.String())
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", relative, err)
	}
	artifacts.Files = append(artifacts.Files, rolloutArtifactFile{
		Path:        filepath.ToSlash(relative),
		Description: description,
	})
	return nil
}

func indentJSON(out *strings.Builder, content []byte) error {
	var value any
	if err := json.Unmarshal(content, &value); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = out.Write(encoded)
	return err
}

// printRolloutArtifacts renders the written files as a tree with their sizes.
//
// The path alone is not enough to act on: the point of writing four files instead of one
// is that they are read differently, and a caller cannot tell which to open without
// seeing the sizes. The tree makes "the tokens are in sequences/0.json, and they are 77
// KB" visible without a second command.
func printRolloutArtifacts(
	out interface{ Write([]byte) (int, error) },
	artifacts *rolloutArtifacts,
) error {
	if artifacts == nil || len(artifacts.Files) == 0 {
		return nil
	}

	display, err := relativeToWorkingDir(artifacts.Dir)
	if err != nil {
		display = artifacts.Dir
	}
	if _, err := fmt.Fprintf(out, "\nArtifacts: %s\n", display); err != nil {
		return err
	}

	for _, line := range renderArtifactTree(artifacts) {
		if _, err := fmt.Fprintf(out, "%s\n", line); err != nil {
			return err
		}
	}
	return nil
}

// renderArtifactTree lays the written files out as a one-level-deep tree, each row
// carrying its size and what it is for. Files sort before directories so the entry point
// is the first thing read.
func renderArtifactTree(artifacts *rolloutArtifacts) []string {
	files := make([]rolloutArtifactFile, 0, len(artifacts.Files))
	directories := make([]string, 0)
	grouped := map[string][]rolloutArtifactFile{}

	for _, file := range artifacts.Files {
		directory, _ := splitArtifactPath(file.Path)
		if directory == "" {
			files = append(files, file)
			continue
		}
		if _, seen := grouped[directory]; !seen {
			directories = append(directories, directory)
		}
		grouped[directory] = append(grouped[directory], file)
	}

	slices.SortFunc(files, func(a, b rolloutArtifactFile) int { return cmp.Compare(a.Path, b.Path) })
	slices.Sort(directories)

	// Rows are built whole — tree prefix and name together — so one padding pass aligns
	// every size and description regardless of how deep the row sits. Padding a name
	// alone cannot: a nested row's prefix is wider, and the columns drift by exactly that
	// difference.
	type row struct {
		label       string
		size        string
		description string
	}
	rows := make([]row, 0, len(artifacts.Files)+len(directories))

	entries := make([]rolloutArtifactFile, 0, len(files)+len(directories))
	entries = append(entries, files...)
	for _, directory := range directories {
		entries = append(entries, rolloutArtifactFile{
			Path:        directory + "/",
			Description: artifactDirectoryDescription(directory),
		})
	}

	for i, entry := range entries {
		branch, indent := "├── ", "│   "
		if i == len(entries)-1 {
			branch, indent = "└── ", "    "
		}

		children, isDir := grouped[strings.TrimSuffix(entry.Path, "/")]
		if !isDir {
			rows = append(rows, row{
				label:       "  " + branch + entry.Path,
				size:        formatBytes(artifactSize(artifacts.Dir, entry.Path)),
				description: entry.Description,
			})
			continue
		}

		rows = append(rows, row{label: "  " + branch + entry.Path, description: entry.Description})
		slices.SortFunc(children, func(a, b rolloutArtifactFile) int { return cmp.Compare(a.Path, b.Path) })
		for j, child := range children {
			childBranch := "├── "
			if j == len(children)-1 {
				childBranch = "└── "
			}
			_, name := splitArtifactPath(child.Path)
			rows = append(rows, row{
				label:       "  " + indent + childBranch + name,
				size:        formatBytes(artifactSize(artifacts.Dir, child.Path)),
				description: child.Description,
			})
		}
	}

	// Width is counted in runes, not bytes: the box-drawing prefixes are multi-byte, and
	// fmt pads strings by rune count.
	labelWidth := 0
	for _, item := range rows {
		labelWidth = max(labelWidth, utf8.RuneCountInString(item.label))
	}

	lines := make([]string, 0, len(rows))
	for _, item := range rows {
		line := fmt.Sprintf("%-*s  %9s", labelWidth, item.label, item.size)
		if item.description != "" {
			line += "  " + item.description
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines
}

// artifactDirectoryDescription names what a directory groups. Only `sequences` exists
// today; an unknown one still gets a row rather than a blank.
func artifactDirectoryDescription(directory string) string {
	if directory == "sequences" {
		return "one root-to-leaf path each — the unit a trainer consumes"
	}
	return ""
}

func splitArtifactPath(relative string) (directory string, name string) {
	if index := strings.LastIndex(relative, "/"); index >= 0 {
		return relative[:index], relative[index+1:]
	}
	return "", relative
}

func artifactSize(root string, relative string) int64 {
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		return 0
	}
	return info.Size()
}

// formatBytes renders a size the way a reader compares two of them: at most one
// decimal, so 77.5 KB and 1.2 MB line up.
func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TB", value/unit)
}

// relativeToWorkingDir prefers a path the reader can paste straight back into a shell.
func relativeToWorkingDir(path string) (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(workingDir, path)
	if err != nil || strings.HasPrefix(relative, "..") {
		return path, nil //nolint:nilerr // an absolute path is a valid answer here
	}
	return relative, nil
}
