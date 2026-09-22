// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"azureaieval/internal/pkg/eval_api"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reportingCommand is a command whose output a test can read back.
func reportingCommand(t *testing.T, jsonOutput bool) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{Use: "create"}
	cmd.Flags().StringP("output", "o", "", "")
	if jsonOutput {
		require.NoError(t, cmd.Flags().Set("output", "json"))
	}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetContext(t.Context())
	return cmd, buf
}

// testPortalPrefix is a prefix built the way the command builds one.
func testPortalPrefix(t *testing.T) *eval_api.PortalPrefix {
	t.Helper()
	prefix, err := eval_api.NewPortalPrefix(
		"/subscriptions/00000000-0000-0000-0000-000000000000" +
			"/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj")
	require.NoError(t, err)
	require.NotNil(t, prefix)
	return prefix
}

// `eval create` printed the id and stopped, so the reader was left holding an
// identifier and no way to look at what it named. ADO 5571804.
func TestReportEvalCreated_LinksToThePortalForACreatedEval(t *testing.T) {
	t.Parallel()

	cmd, out := reportingCommand(t, false)

	require.NoError(t, reportEvalCreated(cmd, "quality", "eval-123", true, testPortalPrefix(t)))

	assert.Contains(t, out.String(), "eval-123", "the id is still reported")
	assert.Contains(t, out.String(), "/build/evaluations/eval-123",
		"and the reader is told where to look at it")
}

// The link is shown for an unchanged eval too. A repeated create is where a
// reader most often is, and it is the same eval they wanted to open.
func TestReportEvalCreated_LinksToThePortalForAnUnchangedEval(t *testing.T) {
	t.Parallel()

	cmd, out := reportingCommand(t, false)

	require.NoError(t, reportEvalCreated(cmd, "quality", "eval-123", false, testPortalPrefix(t)))

	assert.Contains(t, out.String(), "/build/evaluations/eval-123")
}

// A project whose resource id could not be read has no link to give. The link
// is an extra, so the report still has to happen without it.
func TestReportEvalCreated_ReportsWithoutALinkWhenThereIsNoPrefix(t *testing.T) {
	t.Parallel()

	cmd, out := reportingCommand(t, false)

	require.NoError(t, reportEvalCreated(cmd, "quality", "eval-123", true, nil))

	assert.Contains(t, out.String(), "eval-123")
	assert.NotContains(t, out.String(), "/build/evaluations/",
		"a guessed link is worse than none")
}

// -o json is a document, and prose in front of it is not one. The link is
// human narration, so it must not reach a machine-readable stdout.
func TestReportEvalCreated_EmitsOnlyTheDocumentUnderJSON(t *testing.T) {
	t.Parallel()

	cmd, out := reportingCommand(t, true)

	require.NoError(t, reportEvalCreated(cmd, "quality", "eval-123", true, testPortalPrefix(t)))

	var doc map[string]string
	require.NoError(t, json.Unmarshal(out.Bytes(), &doc),
		"stdout has to parse as the document on its own")
	assert.Equal(t, "eval-123", doc["id"])
	assert.Equal(t, "quality", doc["name"])
}
