// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestFormatter_Kinds(t *testing.T) {
	t.Parallel()
	require.Equal(t, JsonFormat, (&JsonFormatter{}).Kind())
	require.Equal(t, EnvVarsFormat, (&EnvVarsFormatter{}).Kind())
	require.Equal(t, TableFormat, (&TableFormatter{}).Kind())
	require.Equal(t, NoneFormat, (&NoneFormatter{}).Kind())
}

func TestNewFormatter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		format   string
		wantKind Format
		wantErr  bool
	}{
		{"json", "json", JsonFormat, false},
		{"table", "table", TableFormat, false},
		{"dotenv", "dotenv", EnvVarsFormat, false},
		{"none", "none", NoneFormat, false},
		{"unsupported", "xml", "", true},
		{"empty", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, err := NewFormatter(tc.format)
			if tc.wantErr {
				require.Error(t, err)
				require.Nil(t, f)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, f)
			require.Equal(t, tc.wantKind, f.Kind())
		})
	}
}

type errWriter struct{}

func TestAddOutputFlag_RegistersHiddenFlag(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	var target string
	AddOutputFlag(cmd.Flags(), &target, []Format{JsonFormat, TableFormat}, TableFormat)

	f := cmd.Flags().Lookup("output")
	require.NotNil(t, f)
	require.True(t, f.Hidden)
	require.Equal(t, "table", f.DefValue)

	// Annotation contains the supported formats.
	ann, ok := f.Annotations[supportedFormatterAnnotation]
	require.True(t, ok)
	require.ElementsMatch(t, []string{"json", "table"}, ann)
}

func TestAddOutputParam_ReturnsCmd(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	out := AddOutputParam(cmd, []Format{JsonFormat}, JsonFormat)
	require.Same(t, cmd, out)
	require.NotNil(t, cmd.Flags().Lookup("output"))
}

func TestGetCommandFormatter_NoOutputFlag_ReturnsNoneFormatter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	f, err := GetCommandFormatter(cmd)
	require.NoError(t, err)
	require.NotNil(t, f)
	require.Equal(t, NoneFormat, f.Kind())
}

func TestGetCommandFormatter_SelectsFormatter(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	AddOutputParam(cmd, []Format{JsonFormat, TableFormat}, JsonFormat)
	require.NoError(t, cmd.ParseFlags([]string{"--output", "table"}))
	f, err := GetCommandFormatter(cmd)
	require.NoError(t, err)
	require.Equal(t, TableFormat, f.Kind())
}

func TestGetCommandFormatter_CaseInsensitiveAndTrimmed(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	AddOutputParam(cmd, []Format{JsonFormat}, JsonFormat)
	require.NoError(t, cmd.ParseFlags([]string{"--output", "  JSON  "}))
	f, err := GetCommandFormatter(cmd)
	require.NoError(t, err)
	require.Equal(t, JsonFormat, f.Kind())
}

func TestGetCommandFormatter_UnsupportedFormat(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	AddOutputParam(cmd, []Format{JsonFormat}, JsonFormat)
	require.NoError(t, cmd.ParseFlags([]string{"--output", "table"}))
	_, err := GetCommandFormatter(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported format")
}

func TestGetCommandFormatter_QueryRequiresJson(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	AddOutputParam(cmd, []Format{JsonFormat, TableFormat}, TableFormat)
	AddQueryParam(cmd)
	require.NoError(t, cmd.ParseFlags([]string{"--output", "table", "--query", "foo"}))
	_, err := GetCommandFormatter(cmd)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--query requires --output json")
}

func TestGetCommandFormatter_NoAnnotations(t *testing.T) {
	t.Parallel()
	// Add the --output flag without the annotation to exercise the fallback
	// path where no supportedFormatters annotation exists.
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().StringP("output", "o", "json", "desc")
	f, err := GetCommandFormatter(cmd)
	require.NoError(t, err)
	require.Equal(t, JsonFormat, f.Kind())
}
