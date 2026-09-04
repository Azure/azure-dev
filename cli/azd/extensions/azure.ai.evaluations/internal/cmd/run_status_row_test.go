// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The detail view drops a field with no value, which is right for a field the
// reader does not need and wrong for the one the command exists to report.
// `status` is omitempty on the wire, and `run cancel` already carries a guard
// for the same reason.
func TestRunShowSaysWhenTheServiceReportedNoStatus(t *testing.T) {
	assert.Equal(t, "not reported", reportedStatus(""),
		"a missing status has to read as missing, not as a row nobody rendered")
	assert.Equal(t, "completed", reportedStatus("completed"),
		"a status the service did send is passed through untouched")
}

// And the row survives the renderer that would otherwise drop it.
func TestARunWithNoStatusStillShowsTheStatusRow(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, emitDetail(&buf, []field{
		{"Run", "evalrun_1"},
		{"Status", reportedStatus("")},
	}))

	assert.Contains(t, buf.String(), "Status")
	assert.Contains(t, buf.String(), "not reported")
}
