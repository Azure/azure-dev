// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"azureaieval/internal/messages"

	"github.com/stretchr/testify/assert"
)

// The results spec counts test cases, not "items". "Items" was also the
// export's own word for JSON elements, so the same noun stood for two
// different numbers in the same command's output.
func TestResultsSpeakInTestCases(t *testing.T) {
	assert.Equal(t, "15 test cases: 9 passed, 6 failed, 0 errored, 0 skipped\n",
		messages.ItemResultTotals(15, 9, 6, 0, 0))

	assert.Contains(t, messages.ScoredPassRateLine(9, 15), "scored test cases")

	assert.Equal(t, "15 test cases x 2 evaluators = 30 evaluator results\n\n",
		messages.CriterionResultReconciliation(15, 2, 30))
}

// "3 of 15 items are failed" made the status an adjective and needed a reader
// to translate it. The status is the verb.
func TestAFilteredListingReadsAsASentence(t *testing.T) {
	assert.Equal(t, "\n6 of 15 test cases failed\n",
		messages.FilteredItemCount(6, 15, "failed"))
	assert.Equal(t, "\n2 of 15 test cases errored\n",
		messages.FilteredItemCount(2, 15, "errored"))
}

// The export announced a file and never said what went into it, so a run that
// returned nothing produced a cheerful line about an empty document.
func TestTheExportSaysHowMuchItWrote(t *testing.T) {
	assert.Equal(t, "Exported 15 test cases to out/run.json\n",
		messages.ExportedTestCases(15, filepath.Join("out", "run.json")))
	assert.Contains(t, messages.ExportedTestCases(0, "out.json"), "0 test cases",
		"an empty export is the one most worth saying out loud")
}

// The separator conversion only has anything to convert on Windows: on Linux a
// backslash is an ordinary character in a filename and filepath.ToSlash leaves
// it alone, correctly. Written as a literal above, this case fails on Linux for
// a reason that says nothing about the message.
func TestTheExportedPathReadsWithForwardSlashes(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("filepath.ToSlash only rewrites the platform separator")
	}

	assert.Equal(t, "Exported 15 test cases to out/run.json\n",
		messages.ExportedTestCases(15, `out\run.json`))
}
