// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build live

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A rubric is the other kind of evaluator: a JSON file of weighted dimensions,
// graded by a judge model rather than by code. It shares nothing with the code
// path on the wire beyond the route, so publishing one had never been
// exercised against a real project.

// writeRubric lays down a rubric file and returns its path.
func writeRubric(t *testing.T, dimensions string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rubric.json")
	require.NoError(t, os.WriteFile(path,
		[]byte(`{"dimensions":`+dimensions+`}`), 0o600))
	return path
}

// evaluatorDocument is what `evaluator show` prints.
type evaluatorDocument struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	EvaluatorType string `json:"evaluator_type"`
	Definition    struct {
		Type       string `json:"type"`
		Dimensions []struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Weight      int    `json:"weight"`
		} `json:"dimensions"`
		DataSchema     map[string]any `json:"data_schema"`
		InitParameters map[string]any `json:"init_parameters"`
		Metrics        map[string]any `json:"metrics"`
	} `json:"definition"`
	SupportedEvaluationLevels []string `json:"supported_evaluation_levels"`
}

// TestCLIRubricRoundTrip publishes a rubric, reads it back, and republishes it.
func TestCLIRubricRoundTrip(t *testing.T) {
	name := uniqueName("azdcli_rubric")
	rubric := writeRubric(t, `[
		{"id":"tone","description":"Is the answer polite?","weight":5},
		{"id":"accuracy","description":"Is the answer correct?","weight":10}
	]`)

	created := requireSuccess(t, run(t, "evaluator", "create", "--name", name, "--rubric", rubric))
	require.Contains(t, created.Stdout, "version 1")
	t.Cleanup(func() {
		run(t, "evaluator", "delete", "--name", name, "--version", "1")
	})

	shown := requireSuccess(t, run(t, "evaluator", "show", "--name", name))
	var doc evaluatorDocument
	shown.JSON(t, &doc)

	require.Equal(t, name, doc.Name)
	require.Equal(t, "1", doc.Version)
	require.Equal(t, "custom", doc.EvaluatorType)
	require.Equal(t, "rubric", doc.Definition.Type,
		"the discriminator is what tells the service which definition kind it holds")

	require.Len(t, doc.Definition.Dimensions, 2)
	byID := map[string]int{}
	for _, d := range doc.Definition.Dimensions {
		byID[d.ID] = d.Weight
		require.NotEmpty(t, d.Description, "a dimension's description is what the judge grades against")
	}
	require.Equal(t, 5, byID["tone"])
	require.Equal(t, 10, byID["accuracy"])

	// The rubric named only dimensions. Everything else is filled in by the
	// service, and a caller reading the definition back gets those defaults
	// rather than what was sent — including the judge model the evaluator will
	// require at run time.
	require.NotEmpty(t, doc.Definition.DataSchema,
		"the service supplies a rubric's data schema; the author never writes one")
	require.NotEmpty(t, doc.Definition.InitParameters)
	require.NotEmpty(t, doc.Definition.Metrics)
	require.NotEmpty(t, doc.SupportedEvaluationLevels)

	// Every registration publishes a new immutable version, which is why there
	// is no separate update command.
	republished := requireSuccess(t, run(t, "evaluator", "create", "--name", name, "--rubric", rubric))
	require.Contains(t, republished.Stdout, "version 2",
		"re-registering must advance the version rather than overwrite")
	t.Cleanup(func() {
		run(t, "evaluator", "delete", "--name", name, "--version", "2")
	})

	// The earlier version stays reachable, which is what makes a published
	// version safe to reference from a config.
	pinned := requireSuccess(t, run(t, "evaluator", "show", "--name", name, "--version", "1"))
	var first evaluatorDocument
	pinned.JSON(t, &first)
	require.Equal(t, "1", first.Version)
}

// TestCLIRubricWeightMustBeAnIntegerFromOneToTen covers the validation a
// hand-authored rubric is most likely to trip.
//
// The service runs two separate checks and they answer differently: a
// fractional weight is rejected for not being an integer, an out-of-range one
// for being out of range. Both are asserted because a caller only ever sees
// one of them, and both have to say what a legal weight is.
func TestCLIRubricWeightMustBeAnIntegerFromOneToTen(t *testing.T) {
	cases := []struct {
		name   string
		weight string
	}{
		{"fractional", "2.5"},
		{"zero", "0"},
		{"above ten", "11"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rubric := writeRubric(t,
				`[{"id":"tone","description":"Is the answer polite?","weight":`+tc.weight+`}]`)

			r := requireFailure(t, run(t, "evaluator", "create",
				"--name", uniqueName("azdcli_badweight"), "--rubric", rubric))
			require.Contains(t, r.Combined(), "between 1 and 10",
				"the refusal must say what a legal weight is")
		})
	}

	// A weight the service accepts, so the cases above are failing on the
	// weight rather than on the rubric shape they share.
	name := uniqueName("azdcli_goodweight")
	ok := writeRubric(t, `[{"id":"tone","description":"Is the answer polite?","weight":1}]`)
	requireSuccess(t, run(t, "evaluator", "create", "--name", name, "--rubric", ok))
	t.Cleanup(func() {
		run(t, "evaluator", "delete", "--name", name, "--version", "1")
	})
}

// TestCLIRubricNeedsDimensions covers the local check, which costs nothing and
// names the field the service would not.
func TestCLIRubricNeedsDimensions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rubric.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"criteria":[]}`), 0o600))

	r := requireFailure(t, run(t, "evaluator", "create",
		"--name", uniqueName("azdcli_nodims"), "--rubric", path))
	require.Contains(t, r.Combined(), "dimensions")
}

// TestCLIRubricRejectsCodeOnlyFlags asserts the settings that would be
// accepted and then dropped are refused instead. A rubric runs no code and its
// schemas come from the service, so carrying them would be a silent no-op.
func TestCLIRubricRejectsCodeOnlyFlags(t *testing.T) {
	rubric := writeRubric(t, `[{"id":"tone","description":"polite","weight":5}]`)
	schema := filepath.Join(t.TempDir(), "schema.json")
	require.NoError(t, os.WriteFile(schema, []byte(`{"type":"object"}`), 0o600))

	r := requireFailure(t, run(t, "evaluator", "create",
		"--name", uniqueName("azdcli_rubricflags"), "--rubric", rubric,
		"--data-schema", schema))
	require.Contains(t, r.Combined(), "--data-schema")
	require.Contains(t, r.Combined(), "--file")
}

// TestCLIEvaluatorShowAcceptsAFullDocument proves `evaluator show` emits JSON a
// script can consume, whatever the definition kind. It renders the service's
// body rather than a typed struct, so nothing else pins that it stays parseable.
func TestCLIEvaluatorShowAcceptsAFullDocument(t *testing.T) {
	name := uniqueName("azdcli_rubricdoc")

	// The wrapped form: a whole evaluator document rather than a bare
	// definition. Both are accepted, and generated rubrics arrive wrapped.
	path := filepath.Join(t.TempDir(), "rubric.json")
	require.NoError(t, os.WriteFile(path, []byte(
		`{"name":"ignored","definition":{"dimensions":[{"id":"tone","description":"polite","weight":3}]}}`,
	), 0o600))

	requireSuccess(t, run(t, "evaluator", "create", "--name", name, "--rubric", path))
	t.Cleanup(func() {
		run(t, "evaluator", "delete", "--name", name, "--version", "1")
	})

	shown := requireSuccess(t, run(t, "evaluator", "show", "--name", name))
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(shown.Stdout), &raw),
		"evaluator show must emit parseable JSON:\n%s", shown.Stdout)

	// The flag names the evaluator, so a name inside the file must not win.
	require.Equal(t, name, raw["name"],
		"--name must decide the evaluator's name, not the document's own field")
	require.NotContains(t, strings.ToLower(shown.Stdout), `"name": "ignored"`)
}
