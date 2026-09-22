// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package rollouts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testID = "3c27c30f5fba261c3a7a3e856b4e1388"

func fixture(t *testing.T, summary, graph string) *ArtifactReader {
	t.Helper()
	root := t.TempDir()
	directory := filepath.Join(root, testID)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "summary.json"), []byte(summary), 0o600); err != nil {
		t.Fatal(err)
	}
	if graph != "" {
		if err := os.WriteFile(filepath.Join(directory, "rollout.json"), []byte(graph), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &ArtifactReader{OutputDir: root}
}

func TestArtifactsAssembleExistingFilesWithoutLosingNumbers(t *testing.T) {
	reader := fixture(t, `{"rollout_id":"`+testID+`","reward":0.12345678901234567890,
		"episode":{"ungraded":true,"steps":[]},"result":{"huge":9007199254740993},
		"artifact":{"version":1,"saved_at":"2026-09-21T20:00:00Z","has_graph":true,
		"environment":{"name":"math_rl","version":"2.1.0"}}}`,
		`{"turns":[],"future_field":9007199254740993,"sequences":[]}`)
	snapshot, err := reader.Get(t.Context(), testID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"0.12345678901234567890", "9007199254740993", `"ungraded":true`, `"future_field"`} {
		if !strings.Contains(string(snapshot.Response), want) {
			t.Fatalf("missing original value %s: %s", want, snapshot.Response)
		}
	}
	if snapshot.Environment == nil || snapshot.Environment.Name != "math_rl" ||
		snapshot.Environment.Version != "2.1.0" || snapshot.SavedAt.IsZero() || len(snapshot.Warnings) != 0 {
		t.Fatalf("incorrect provenance: %+v", snapshot)
	}
	if bytes.Contains(snapshot.Response, []byte(`"artifact"`)) || bytes.Contains(snapshot.Response, []byte(`"success"`)) {
		t.Fatal("local metadata or an invented verdict leaked into response")
	}
}

func TestArtifactsLegacyAndOptionalSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		want         *bool
		warning      bool
	}{
		{"absent", "", nil, false},
		{"legacy false", `,"success":false`, nil, true},
		{"legacy true", `,"success":true`, new(true), false},
		{"new false", `,"success":false,"artifact":{"version":1,"saved_at":"2026-09-21T20:00:00Z"}`, new(false), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := fixture(t, `{"rollout_id":"`+testID+`","reward":1`+tc.fields+`}`, `{}`)
			snapshot, err := reader.Get(t.Context(), testID)
			if err != nil {
				t.Fatal(err)
			}
			response, err := decodeResponse(snapshot.Response)
			if err != nil {
				t.Fatal(err)
			}
			if (response.Success == nil) != (tc.want == nil) ||
				(tc.want != nil && *response.Success != *tc.want) {
				t.Fatalf("incorrect verdict: %v", response.Success)
			}
			if (len(snapshot.Warnings) > 0) != tc.warning {
				t.Fatalf("incorrect warnings: %v", snapshot.Warnings)
			}
			data, err := os.ReadFile(filepath.Join(reader.OutputDir, testID, "summary.json"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "legacy false" && !bytes.Contains(data, []byte(`"success":false`)) {
				t.Fatal("reader changed the original summary")
			}
		})
	}
}

func TestArtifactsRejectInvalidFiles(t *testing.T) {
	for _, tc := range []struct{ name, summary, graph string }{
		{"corrupt summary", `{`, `{}`},
		{"null summary", `null`, `{}`},
		{"missing reward", `{"rollout_id":"` + testID + `"}`, `{}`},
		{"wrong ID", `{"rollout_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reward":1}`, `{}`},
		{"invalid verdict", `{"rollout_id":"` + testID + `","reward":1,"success":"false"}`, `{}`},
		{"future version", `{"rollout_id":"` + testID + `","reward":1,"artifact":{"version":2}}`, `{}`},
		{"bad environment", `{"rollout_id":"` + testID + `","reward":1,
			"artifact":{"version":1,"saved_at":"2026-09-21T20:00:00Z","environment":{"name":"math_rl"}}}`, `{}`},
		{"missing promised graph", `{"rollout_id":"` + testID + `","reward":1,
			"artifact":{"version":1,"saved_at":"2026-09-21T20:00:00Z","has_graph":true}}`, ""},
		{"malformed graph", `{"rollout_id":"` + testID + `","reward":1}`, `{`},
		{"null graph", `{"rollout_id":"` + testID + `","reward":1}`, `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := fixture(t, tc.summary, tc.graph)
			if _, err := reader.Get(t.Context(), testID); err == nil {
				t.Fatal("expected explicit read error")
			}
		})
	}
}

func TestArtifactsMissingGraphIsReported(t *testing.T) {
	reader := fixture(t, `{"rollout_id":"`+testID+`","reward":1}`, "")
	snapshot, err := reader.Get(t.Context(), testID)
	if err != nil || len(snapshot.Warnings) != 1 {
		t.Fatalf("summary-only result must explain missing graph: %+v, %v", snapshot, err)
	}
	if !snapshot.SavedAt.IsZero() || snapshot.Environment != nil {
		t.Fatal("legacy context must not be invented")
	}
}

func TestArtifactsPathAndCancellation(t *testing.T) {
	reader := &ArtifactReader{OutputDir: t.TempDir()}
	for _, id := range []string{"", "..", "../outside", `..\outside`, strings.ToUpper(testID)} {
		if _, err := reader.Get(t.Context(), id); err == nil {
			t.Fatalf("expected invalid ID error: %s", id)
		}
	}
	if _, err := reader.Get(t.Context(), testID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected missing-artifacts error: %v", err)
	}
	if _, err := reader.Get(t.Context(), testID); !errors.Is(err, ErrArtifactDirectoryNotFound) {
		t.Fatalf("expected distinct missing-directory error: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reader.Get(ctx, testID); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
}

func TestResponsePreservesOptionalVerdict(t *testing.T) {
	for _, tc := range []struct {
		field string
		want  *bool
	}{
		{"", nil}, {`,"success":false`, new(false)}, {`,"success":true`, new(true)},
	} {
		raw := json.RawMessage(`{"rollout_id":"` + testID + `","reward":1` + tc.field + `}`)
		response, err := decodeResponse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if (response.Success == nil) != (tc.want == nil) || (tc.want != nil && *response.Success != *tc.want) {
			t.Fatal("optional verdict changed")
		}
		if !bytes.Equal(response.Raw, raw) {
			t.Fatal("raw response changed")
		}
	}
}
