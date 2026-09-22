// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rollouts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArtifactVersion marks exports that preserve optional verdicts and publish the summary last.
const ArtifactVersion = 1

// ErrArtifactDirectoryNotFound distinguishes an absent rollout from an incomplete export.
var ErrArtifactDirectoryNotFound = errors.New("rollout artifact directory does not exist")

// Reader hides local artifact or remote retrieval from the dashboard.
type Reader interface {
	Get(ctx context.Context, rolloutID string) (Snapshot, error)
}

// Snapshot separates presentation metadata from the reconstructed execute response.
type Snapshot struct {
	Response    json.RawMessage `json:"response"`
	SavedAt     time.Time       `json:"saved_at,omitzero"`
	Source      string          `json:"source"`
	Environment *Environment    `json:"environment,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
}

// Environment identifies the published target recorded from the execution request.
type Environment struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ArtifactMetadata describes the export, not the outcome of the environment task.
type ArtifactMetadata struct {
	Version         int          `json:"version"`
	SavedAt         time.Time    `json:"saved_at"`
	ProjectEndpoint string       `json:"project_endpoint,omitempty"`
	Environment     *Environment `json:"environment,omitempty"`
	HasGraph        bool         `json:"has_graph"`
}

// Validate rejects incomplete or unsupported export metadata.
func (m *ArtifactMetadata) Validate() error {
	if m.Version != ArtifactVersion {
		return fmt.Errorf("unsupported rollout artifact version %d (expected %d)", m.Version, ArtifactVersion)
	}
	if m.SavedAt.IsZero() {
		return fmt.Errorf("rollout artifact metadata is missing its save timestamp")
	}
	if m.Environment != nil &&
		(strings.TrimSpace(m.Environment.Name) == "" || strings.TrimSpace(m.Environment.Version) == "") {
		return fmt.Errorf("rollout environment metadata requires both name and version")
	}
	return nil
}

// ArtifactReader reads the training artifacts without maintaining a second copy or acquiring credentials.
type ArtifactReader struct {
	OutputDir string
}

// Get assembles the response fields in summary.json with the graph in rollout.json.
func (r ArtifactReader) Get(ctx context.Context, rolloutID string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if err := ValidateID(rolloutID); err != nil {
		return Snapshot{}, err
	}
	directory := filepath.Join(r.OutputDir, rolloutID)
	// #nosec G304 -- directory is OutputDir joined with a rolloutID already accepted by ValidateID, and the file name is fixed.
	data, err := os.ReadFile(filepath.Join(directory, "summary.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, directoryErr := os.Stat(directory); errors.Is(directoryErr, os.ErrNotExist) {
				return Snapshot{}, fmt.Errorf("%w: %w", ErrArtifactDirectoryNotFound, err)
			}
		}
		return Snapshot{}, fmt.Errorf("read rollout summary in %s (check --output-dir): %w", directory, err)
	}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(data, &summary); err != nil || summary == nil {
		return Snapshot{}, fmt.Errorf("invalid rollout summary in %s: expected a JSON object", directory)
	}
	snapshot := Snapshot{Source: "Local rollout artifacts"}
	var metadata *ArtifactMetadata
	if raw, ok := summary["artifact"]; ok {
		var decoded ArtifactMetadata
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return Snapshot{}, fmt.Errorf("invalid rollout artifact metadata in %s", directory)
		}
		if err := decoded.Validate(); err != nil {
			return Snapshot{}, err
		}
		metadata = &decoded
		snapshot.Environment, snapshot.SavedAt = metadata.Environment, metadata.SavedAt
	}
	response := make(map[string]json.RawMessage)
	for _, key := range []string{"rollout_id", "reward", "success", "result", "episode"} {
		if raw, ok := summary[key]; ok {
			response[key] = raw
		}
	}
	// Older exporters used a plain bool and could not distinguish omitted success from false.
	if metadata == nil && strings.TrimSpace(string(response["success"])) == "false" {
		delete(response, "success")
		snapshot.Warnings = append(snapshot.Warnings,
			"Task verdict unavailable: this older export may have defaulted missing success to false. "+
				"The original value is retained in summary.json.")
	}
	// #nosec G304 -- directory is OutputDir joined with a rolloutID already accepted by ValidateID, and the file name is fixed.
	graph, err := os.ReadFile(filepath.Join(directory, "rollout.json"))
	switch {
	case err == nil:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(graph, &fields); err != nil || fields == nil {
			return Snapshot{}, fmt.Errorf("invalid capture graph in %s: expected a JSON object", directory)
		}
		response["rollout"] = graph
	case errors.Is(err, os.ErrNotExist) && (metadata == nil || !metadata.HasGraph):
		snapshot.Warnings = append(snapshot.Warnings,
			"Capture graph is unavailable; showing only the saved summary.")
	case err != nil:
		return Snapshot{}, fmt.Errorf("read rollout graph in %s: %w", directory, err)
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	snapshot.Response, err = json.Marshal(response)
	if err != nil {
		return Snapshot{}, fmt.Errorf("assemble rollout artifacts: %w", err)
	}
	decoded, err := decodeResponse(snapshot.Response)
	if err != nil {
		return Snapshot{}, fmt.Errorf("invalid rollout summary: %w", err)
	}
	if decoded.RolloutID != rolloutID {
		return Snapshot{}, fmt.Errorf("saved rollout ID does not match the requested ID")
	}
	return snapshot, nil
}
