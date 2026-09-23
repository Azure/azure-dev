// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import "fmt"

// RunFollowUp is the commands that turn a finished run into something a reader
// can act on.
//
// Every terminal run offers the unfiltered listing and export. A failed-only
// listing is additional guidance, never the only way to read the results.
//
// The commands carry --eval and --run. Printing a bare command means the reader
// has to find two ids and retype them, which is the point at which most people
// go to the portal instead.
//
// Errored rows are not offered --failed-only. That filter deliberately holds
// back rows nothing scored, so it would open an empty list for the run that
// most needs reading.
func RunFollowUp(eval, runID string, failed, errored bool) string {
	out := "\nView available results:\n"
	if failed || errored {
		out = "\nInvestigate:\n"
	}
	out += allRowsCommand(eval, runID)
	if failed {
		out += failingRowsCommand(eval, runID)
	}
	if failed && errored {
		out += "\nRows that errored were never scored, so the failing-row listing does not hold them.\n"
	}
	return out + exportCommand(eval, runID)
}

// FailedRunFollowUp offers diagnostics even when execution produced no rows.
func FailedRunFollowUp(eval, runID string, failedRows bool) string {
	out := "\nInspect available output (the run may have failed before producing rows):\n" +
		allRowsCommand(eval, runID)
	if failedRows {
		out += "\nView failed verdicts:\n" + failingRowsCommand(eval, runID)
	}
	return out + "\nExport run diagnostics and any available results:\n" + exportRunCommand(eval, runID)
}

// RunFollowUpMissingIDs avoids suggesting a command that could select a different run.
func RunFollowUpMissingIDs() string {
	return "\nFollow-up commands require both an eval ID and run ID; one was not available.\n"
}

// The commands below are written out per shape rather than assembled from
// fragments, so that every command word and flag name is a literal. A surface
// test reads these strings and checks each flag against the command that would
// have to accept it, and it can only do that if the text is not stitched
// together at run time.

func failingRowsCommand(eval, runID string) string {
	if eval == "" {
		return fmt.Sprintf("  azd ai eval run output list --run %s --failed-only\n", shellArg(runID))
	}
	return fmt.Sprintf("  azd ai eval run output list --eval %s --run %s --failed-only\n",
		shellArg(eval), shellArg(runID))
}

func allRowsCommand(eval, runID string) string {
	if eval == "" {
		return fmt.Sprintf("  azd ai eval run output list --run %s\n", shellArg(runID))
	}
	return fmt.Sprintf("  azd ai eval run output list --eval %s --run %s\n",
		shellArg(eval), shellArg(runID))
}

func exportCommand(eval, runID string) string {
	return "\nExport complete results:\n" + exportRunCommand(eval, runID)
}

func exportRunCommand(eval, runID string) string {
	if eval == "" {
		return fmt.Sprintf("  azd ai eval run output export --run %s --output-file ./%s.json\n",
			shellArg(runID), shellArg(runID))
	}
	return fmt.Sprintf("  azd ai eval run output export --eval %s --run %s --output-file ./%s.json\n",
		shellArg(eval), shellArg(runID), shellArg(runID))
}
