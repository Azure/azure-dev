// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvVarsFormatterStringMap(t *testing.T) {
	formatter := &EnvVarsFormatter{}

	m := make(map[string]string, 3)
	m["Alpha"] = "1"
	m["Bravo"] = "2"
	m["Charlie"] = "3"

	buffer := &bytes.Buffer{}
	err := formatter.Format(m, buffer, nil)
	require.NoError(t, err)

	expected := "Alpha=1\nBravo=2\nCharlie=3\n"
	require.Equal(t, expected, buffer.String())
}
