// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runEval executes the real command tree, so a guard that is meant to fire
// before the network does is exercised the way a caller reaches it.
func runEval(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	return root.ExecuteContext(context.Background())
}

// RESULTS spec 7.3: a complete export "may contain sensitive prompts,
// responses, messages, tool arguments, and tool results", so the CLI MUST
// "require --output-file" and "allow stdout only through explicit
// --output-file -".
//
// Defaulting to stdout put all of that on the terminal, and into whatever
// scrollback or CI log was capturing it, for anyone who ran the command to see
// what it did.
func TestExportRequiresSomewhereToPutTheDocument(t *testing.T) {
	err := runEval(t, "run", "output", "export", "--eval", "smoke")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "--output-file")
	assert.Contains(t, err.Error(), "-",
		"the refusal has to name the way to ask for stdout deliberately")
}

// RESULTS spec 7.3: "refuse overwrite unless --force is supplied."
//
// The export replaced whatever was at the path, so a second run against the
// wrong --run destroyed the first one's results with nothing said.
func TestExportRefusesToReplaceWhatIsAlreadyThere(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "results.json")
	require.NoError(t, os.WriteFile(dest, []byte(`{"keep":true}`), 0o600))

	err := runEval(t, "run", "output", "export", "--eval", "smoke", "--output-file", dest)

	require.Error(t, err)
	body, readErr := os.ReadFile(dest)
	require.NoError(t, readErr)
	assert.JSONEq(t, `{"keep":true}`, string(body),
		"the refusal has to come before anything could overwrite it")
}

// Both guards are settled before the run is read, so a caller who forgot one
// is not charged two round trips to find out.
func TestExportChecksItsDestinationBeforeReadingAnything(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "results.json")
	require.NoError(t, os.WriteFile(dest, []byte("{}"), 0o600))

	// No endpoint and no project: reaching the service would fail with a
	// connection error rather than the destination refusal.
	err := runEval(t, "run", "output", "export", "--eval", "smoke", "--output-file", dest)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "endpoint",
		"the local refusal has to come first:\n%v", err)
}

// RESULTS acceptance 22: "CSV and JSONL are rejected with an actionable
// JSON-conversion message." The recipe used to live only in --help, which is
// the one place a caller who just hit the error is not looking.
func TestExportFormatRefusalCarriesTheConversionRecipe(t *testing.T) {
	for _, format := range []string{"csv", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			err := runEval(t, "run", "output", "export",
				"--eval", "smoke", "--output-file", "-", "--format", format)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "not supported")
			assert.Contains(t, err.Error(), "jq",
				"the refusal has to say how to get the shape they asked for")
		})
	}
}

// RESULTS spec 8.1: "Filtering occurs server-side before pagination."
//
// The endpoint takes no status parameter, so the filter runs here -- and it ran
// against one fetched page. A run whose first ten rows passed answered
// `--failed-only` with "no failing rows" while the failures sat on page two.
func TestFilteredPageWalksPastAPageThatMatchesNothing(t *testing.T) {
	pages := map[string]*eval_api.OutputItemList{
		"": {
			Data:    passingItems("oi_1", "oi_2"),
			HasMore: true,
			LastID:  "oi_2",
		},
		"oi_2": {
			Data: []eval_api.OutputItem{failingItem("oi_3")},
		},
	}

	got, err := filteredItemPage(
		context.Background(), nil, "eval_1", "evalrun_1", 2, "",
		map[string]bool{itemFailed: true},
		pagerFrom(pages))

	require.NoError(t, err)
	require.Len(t, got.Data, 1, "the failure on the second page has to be found")
	assert.Equal(t, "oi_3", got.Data[0].ID)
	assert.False(t, got.HasMore, "there is nothing after it")
}

// The walk stops as soon as the page is full, so a failing run still costs one
// request -- and the cursor names the last row actually shown, so resuming
// neither repeats nor skips one.
func TestFilteredPageStopsOnceItHasEnough(t *testing.T) {
	calls := 0
	pages := map[string]*eval_api.OutputItemList{
		"": {
			Data:    []eval_api.OutputItem{failingItem("oi_1"), failingItem("oi_2"), failingItem("oi_3")},
			HasMore: true,
			LastID:  "oi_3",
		},
	}

	got, err := filteredItemPage(
		context.Background(), nil, "eval_1", "evalrun_1", 2, "",
		map[string]bool{itemFailed: true},
		func(_ context.Context, _ *eval_api.EvalClient, _, _ string, _ int, after string,
		) (*eval_api.OutputItemList, error) {
			calls++
			return pages[after], nil
		})

	require.NoError(t, err)
	assert.Equal(t, 1, calls, "one page held enough")
	require.Len(t, got.Data, 2)
	assert.True(t, got.HasMore)
	assert.Equal(t, "oi_2", got.LastID,
		"the cursor is the last row shown, not the last row fetched")
}

// Without a filter nothing changes: one request, one page, the envelope the
// service returned.
func TestUnfilteredPageIsStillOneRequest(t *testing.T) {
	calls := 0
	page := &eval_api.OutputItemList{Data: passingItems("oi_1"), HasMore: true, LastID: "oi_1"}

	got, err := filteredItemPage(
		context.Background(), nil, "eval_1", "evalrun_1", 10, "", nil,
		func(_ context.Context, _ *eval_api.EvalClient, _, _ string, _ int, _ string,
		) (*eval_api.OutputItemList, error) {
			calls++
			return page, nil
		})

	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	assert.Same(t, page, got)
}

func pagerFrom(pages map[string]*eval_api.OutputItemList) itemPager {
	return func(_ context.Context, _ *eval_api.EvalClient, _, _ string, _ int, after string,
	) (*eval_api.OutputItemList, error) {
		return pages[after], nil
	}
}

func passingItems(ids ...string) []eval_api.OutputItem {
	items := make([]eval_api.OutputItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, eval_api.OutputItem{
			ID:      id,
			Results: []eval_api.OutputResult{{Name: "relevance", Passed: new(true), Score: 1}},
		})
	}
	return items
}

func failingItem(id string) eval_api.OutputItem {
	return eval_api.OutputItem{
		ID:      id,
		Results: []eval_api.OutputResult{{Name: "relevance", Passed: new(false), Score: 0}},
	}
}
