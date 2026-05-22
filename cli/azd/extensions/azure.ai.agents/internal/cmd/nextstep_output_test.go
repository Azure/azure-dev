// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaiagent/internal/cmd/nextstep"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriterIsTerminal_NonStdoutWriter(t *testing.T) {
	t.Parallel()

	assert.False(t, writerIsTerminal(&bytes.Buffer{}))
}

func TestPrintNextIfTerminal_SuppressesNonStdoutWriter(t *testing.T) {
	t.Parallel()

	suggestions := []nextstep.Suggestion{
		{Command: "azd ai agent run", Description: "start locally"},
	}

	var buf bytes.Buffer
	require.NoError(t, printNextIfTerminal(&buf, suggestions))
	require.NoError(t, printAllNextIfTerminal(&buf, suggestions))
	assert.Empty(t, buf.String())
}
