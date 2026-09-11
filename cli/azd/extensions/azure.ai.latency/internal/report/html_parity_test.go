// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.latency/internal/model"
)

func TestRenderHTMLMatchesPythonGolden(t *testing.T) {
	for _, name := range []string{
		"within-target",
		"workload-explained",
		"unexplained-gap",
		"no-benchmark-coverage",
		"logs-unavailable",
	} {
		t.Run(name, func(t *testing.T) {
			assertPythonGolden(t, name)
		})
	}
}

func assertPythonGolden(t *testing.T, name string) {
	t.Helper()
	base := "python_" + strings.ReplaceAll(name, "-", "_")
	fixture, err := os.ReadFile( //nolint:gosec // The basename comes from the fixed scenario table.
		filepath.Join("testdata", base+".json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	var result model.AssessmentResult
	if err := json.Unmarshal(fixture, &result); err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile( //nolint:gosec // The basename comes from the fixed scenario table.
		filepath.Join("testdata", base+".html"),
	)
	if err != nil {
		t.Fatal(err)
	}

	var actual bytes.Buffer
	if err := RenderHTML(&actual, []*model.AssessmentResult{&result}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual.Bytes(), expected) {
		index := firstDifferentByte(actual.Bytes(), expected)
		t.Fatalf(
			"rendered HTML differs from Python golden at byte %d\nactual:   %q\nexpected: %q",
			index,
			byteWindow(actual.Bytes(), index),
			byteWindow(expected, index),
		)
	}
}

func TestRenderHTMLMatchesPythonMultiScenarioGolden(t *testing.T) {
	results := []*model.AssessmentResult{
		fixtureAssessment(t, "within-target"),
		fixtureAssessment(t, "workload-explained"),
		fixtureAssessment(t, "unexplained-gap"),
		fixtureAssessment(t, "logs-unavailable"),
	}
	expected, err := os.ReadFile(filepath.Join("testdata", "python_multi_scenario.html"))
	if err != nil {
		t.Fatal(err)
	}
	var actual bytes.Buffer
	if err := RenderHTML(&actual, results); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual.Bytes(), expected) {
		index := firstDifferentByte(actual.Bytes(), expected)
		t.Fatalf(
			"multi-scenario HTML differs from Python golden at byte %d\nactual:   %q\nexpected: %q",
			index,
			byteWindow(actual.Bytes(), index),
			byteWindow(expected, index),
		)
	}
}

func firstDifferentByte(left, right []byte) int {
	limit := min(len(left), len(right))
	for index := range limit {
		if left[index] != right[index] {
			return index
		}
	}
	return limit
}

func byteWindow(value []byte, index int) string {
	start := max(index-80, 0)
	end := min(index+160, len(value))
	return string(value[start:end])
}
