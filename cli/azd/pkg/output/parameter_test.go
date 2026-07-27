// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestAddQueryParam_RegistersHiddenFlag(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	AddQueryParam(cmd)
	f := cmd.Flags().Lookup("query")
	require.NotNil(t, f)
	require.True(t, f.Hidden)
}

func TestGetCommandFormatter_QueryWithJson(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	AddOutputParam(cmd, []Format{JsonFormat}, JsonFormat)
	AddQueryParam(cmd)
	require.NoError(t, cmd.ParseFlags([]string{"--output", "json", "--query", "items[0]"}))
	f, err := GetCommandFormatter(cmd)
	require.NoError(t, err)
	jf, ok := f.(*JsonFormatter)
	require.True(t, ok)
	require.Equal(t, "items[0]", jf.Query)
}
