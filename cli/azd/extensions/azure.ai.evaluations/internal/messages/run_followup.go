// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package messages

import "fmt"

// RunFollowUp is the commands that turn a finished run into something a reader
// can act on.
//
// Printed whenever a run has anything to investigate, not only when rows
// failed. A run whose rows all errored has plenty to look at and used to close
// with nothing but the word "failed" and a count.
//
// The commands carry --eval and --run. Printing a bare command means the reader
// has to find two ids and retype them, which is the point at which most people
// go to the portal instead.
//
// Errored rows are not offered --failed-only. That filter deliberately holds
// back rows nothing scored, so it would open an empty list for the run that
// most needs reading.
func RunFollowUp(eval, runID string, failed, errored bool) string {
	if !failed && !errored {
		return ""
	}

	out := "\nInvestigate:\n"
	if failed {
		out += failingRowsCommand(eval, runID)
	}
	if errored {
		out += allRowsCommand(eval, runID)
	}
	if failed && errored {
		out += "\nRows that errored were never scored, so the failing-row listing does not hold them.\n"
	}
	return out + exportCommand(eval, runID)
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
	if eval == "" {
		return fmt.Sprintf("\nExport complete results:\n"+
			"  azd ai eval run output export --run %s --output-file ./%s.json\n",
			shellArg(runID), shellArg(runID))
	}
	return fmt.Sprintf("\nExport complete results:\n"+
		"  azd ai eval run output export --eval %s --run %s --output-file ./%s.json\n",
		shellArg(eval), shellArg(runID), shellArg(runID))
}
