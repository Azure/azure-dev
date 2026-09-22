// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"azure.ai.rle/internal/rollouts"
)

// defaultRolloutOutputDir groups rollout artifacts by rollout ID.
const defaultRolloutOutputDir = ".output"

// rolloutArtifacts records the files written for one Execute Rollout response.
type rolloutArtifacts struct {
	// Dir is the absolute directory the files were written to.
	Dir string
	// Files is every file written, relative to Dir, in write order.
	Files []rolloutArtifactFile
}

// rolloutArtifactFile is one written file and its terminal description.
type rolloutArtifactFile struct {
	// Path is relative to the artifact directory, slash-separated.
	Path string
	// Description is one crisp line: what it holds, and what reads it.
	Description string
}

// rolloutSummary contains the outcome and capture shape without token arrays.
type rolloutSummary struct {
	RolloutID string          `json:"rollout_id"`
	Reward    float64         `json:"reward"`
	Success   *bool           `json:"success,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`

	Episode  *rollouts.Episode          `json:"episode,omitempty"`
	Artifact *rollouts.ArtifactMetadata `json:"artifact,omitempty"`

	CaptureLevel string          `json:"capture_level,omitempty"`
	RolloutType  string          `json:"rollout_type,omitempty"`
	Trainable    *bool           `json:"trainable,omitempty"`
	Stats        json.RawMessage `json:"stats,omitempty"`
	Validation   json.RawMessage `json:"validation,omitempty"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`

	Sequences []rolloutSequenceSummary `json:"sequences,omitempty"`
}

// rolloutSequenceSummary describes one sequence and points to its array file.
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

// capturedGraph is the graph subset needed for derived artifact files.
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

// capturedSequence is one root-to-leaf path and its token-aligned arrays.
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
func writeRolloutArtifacts(
	outputDir string,
	response *executeRolloutResponse,
	metadata *rollouts.ArtifactMetadata,
) (artifacts *rolloutArtifacts, err error) {
	if response == nil {
		return nil, fmt.Errorf("no rollout response to write")
	}

	id := response.RolloutID
	if err := rollouts.ValidateID(id); err != nil {
		return nil, err
	}
	if metadata == nil {
		metadata = &rollouts.ArtifactMetadata{Version: rollouts.ArtifactVersion, SavedAt: time.Now().UTC()}
	}
	export := *metadata
	if err := export.Validate(); err != nil {
		return nil, err
	}
	if export.ProjectEndpoint != "" {
		endpoint, err := normalizeFoundryProjectEndpoint(export.ProjectEndpoint)
		if err != nil {
			return nil, err
		}
		export.ProjectEndpoint = endpoint
	}
	export.HasGraph = len(response.Rollout) > 0 && string(response.Rollout) != "null"
	root, err := filepath.Abs(filepath.Join(outputDir, id))
	if err != nil {
		return nil, fmt.Errorf("resolve rollout output directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(root), 0o750); err != nil {
		return nil, fmt.Errorf("create rollout output directory: %w", err)
	}
	if err := os.Mkdir(root, 0o750); err != nil {
		return nil, fmt.Errorf("create rollout directory (existing artifacts are never overwritten): %w", err)
	}
	published := false
	defer func() {
		if !published {
			if cleanupErr := os.RemoveAll(root); cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("remove incomplete rollout artifacts: %w", cleanupErr))
			}
		}
	}()
	artifacts = &rolloutArtifacts{Dir: root}

	var graph capturedGraph
	if len(response.Rollout) > 0 {
		// Preserve malformed graphs verbatim; only derived views are skipped.
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
		Artifact:     &export,
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
	if export.HasGraph {
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

	// Preserve optional fields and precise numbers from the response, not the typed projection.
	encoded, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("encode rollout summary: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, fmt.Errorf("decode rollout summary: %w", err)
	}
	if len(response.Raw) > 0 {
		var original map[string]json.RawMessage
		if err := json.Unmarshal(response.Raw, &original); err != nil {
			return nil, fmt.Errorf("decode original rollout response: %w", err)
		}
		for _, key := range []string{"rollout_id", "reward", "success", "result", "episode"} {
			delete(fields, key)
			if value, ok := original[key]; ok {
				fields[key] = value
			}
		}
	}
	// Publishing the summary last marks the artifact set ready for readers.
	if err := writeJSONFile(artifacts, root, "summary.json",
		"outcome, capture stats and the sequence index — start here", fields); err != nil {
		return nil, err
	}
	published = true
	return artifacts, nil
}

func describeSequence(sequence capturedSequence) string {
	role := sequence.Role
	if role == "" {
		role = "sequence"
	}
	if len(sequence.InputIDs) == 0 {
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
	encoded, err := json.Marshal(value)
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
) (err error) {
	path := filepath.Join(root, relative)
	if directory := filepath.Dir(path); directory != root {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(relative), err)
		}
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, content, "", "  "); err == nil {
		content = indented.Bytes()
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".rollout-*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", relative, err)
	}
	defer func() {
		if removeErr := os.Remove(file.Name()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove temporary artifact: %w", removeErr))
		}
	}()
	_, writeErr := file.Write(content)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("write %s: %w", relative, err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("publish %s: %w", relative, err)
	}
	artifacts.Files = append(artifacts.Files, rolloutArtifactFile{
		Path:        filepath.ToSlash(relative),
		Description: description,
	})
	return nil
}

// printRolloutArtifacts renders the written files as a tree with sizes and descriptions.
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

// renderArtifactTree lays files out as a one-level-deep tree.
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
