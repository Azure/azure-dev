// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"encoding/json"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertOutputFlagOptions verifies that cmd has the per-command --output flag
// configuration registered via [azdext.RegisterFlagOptions]. The SDK records
// these as cobra annotations rather than as a redeclared flag value, so we
// inspect cmd.Annotations directly rather than reading from cmd.Flags().
func assertOutputFlagOptions(t *testing.T, cmd *cobra.Command, wantDefault string, wantAllowed []string) {
	t.Helper()
	require.NotNil(t, cmd)
	require.NotNil(t, cmd.Annotations, "cmd.Annotations should be set by RegisterFlagOptions")

	got := cmd.Annotations["azdext.default/output"]
	assert.Equal(t, wantDefault, got, "default for --output")

	allowedJSON := cmd.Annotations["azdext.allowed-values/output"]
	require.NotEmpty(t, allowedJSON, "allowed values for --output should be set")
	var allowed []string
	require.NoError(t, json.Unmarshal([]byte(allowedJSON), &allowed))
	assert.Equal(t, wantAllowed, allowed, "allowed values for --output")
}
