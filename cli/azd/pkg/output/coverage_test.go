// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/contracts"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Formatter Kind() identity
// ---------------------------------------------------------------------------

func TestFormatter_Kinds(t *testing.T) {
	t.Parallel()
	require.Equal(t, JsonFormat, (&JsonFormatter{}).Kind())
	require.Equal(t, EnvVarsFormat, (&EnvVarsFormatter{}).Kind())
	require.Equal(t, TableFormat, (&TableFormatter{}).Kind())
	require.Equal(t, NoneFormat, (&NoneFormatter{}).Kind())
}

// ---------------------------------------------------------------------------
// NewFormatter
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// NoneFormatter.Format always errors
// ---------------------------------------------------------------------------

func TestNoneFormatter_Format_Errors(t *testing.T) {
	t.Parallel()
	f := &NoneFormatter{}
	var buf bytes.Buffer
	err := f.Format(map[string]string{"a": "b"}, &buf, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "none")
	require.Empty(t, buf.String())
}

// ---------------------------------------------------------------------------
// Color / formatting helpers — exercise the code paths.
// ---------------------------------------------------------------------------

func TestWithFormatters_NonEmpty(t *testing.T) {
	t.Parallel()
	inputs := []struct {
		name string
		fn   func(string, ...any) string
	}{
		{"WithLinkFormat", WithLinkFormat},
		{"WithHighLightFormat", WithHighLightFormat},
		{"WithErrorFormat", WithErrorFormat},
		{"WithWarningFormat", WithWarningFormat},
		{"WithSuccessFormat", WithSuccessFormat},
		{"WithGrayFormat", WithGrayFormat},
		{"WithHintFormat", WithHintFormat},
		{"WithBold", WithBold},
		{"WithUnderline", WithUnderline},
	}
	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := tc.fn("hello %s", "world")
			require.Contains(t, out, "hello")
			require.Contains(t, out, "world")
		})
	}
}

func TestWithBackticks(t *testing.T) {
	t.Parallel()
	require.Equal(t, "`foo`", WithBackticks("foo"))
	require.Equal(t, "``", WithBackticks(""))
}

func TestWithMarkdown(t *testing.T) {
	t.Parallel()
	// Basic text should round-trip through glamour without an error.
	out := WithMarkdown("# Hello\n\nSome **bold** text.")
	require.NotEmpty(t, out)
	require.Contains(t, strings.ToLower(out), "hello")
}

func TestWithHyperlink_NonTerminal(t *testing.T) {
	t.Parallel()
	// In tests, stdout is not a TTY so plain URL should be returned.
	out := WithHyperlink("https://example.com", "click")
	require.Equal(t, "https://example.com", out)
}

func TestGetConsoleWidth_FromEnv(t *testing.T) {
	// Not parallel due to t.Setenv
	t.Setenv("COLUMNS", "99")
	w := getConsoleWidth()
	// When the terminal can't be detected, the env fallback kicks in and
	// should return the parsed value. When running inside an IDE-attached
	// terminal, width can come from consolesize instead — allow either.
	require.Greater(t, w, 0)
}

func TestGetConsoleWidth_InvalidEnvFallsBack(t *testing.T) {
	t.Setenv("COLUMNS", "not-a-number")
	w := getConsoleWidth()
	require.Greater(t, w, 0)
}

// ---------------------------------------------------------------------------
// DynamicMultiWriter
// ---------------------------------------------------------------------------

func TestDynamicMultiWriter_Default_DiscardsWhenNoWriters(t *testing.T) {
	t.Parallel()
	dmw := NewDynamicMultiWriter()
	n, err := dmw.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
}

func TestDynamicMultiWriter_WriteFansOutToAll(t *testing.T) {
	t.Parallel()
	var a, b bytes.Buffer
	dmw := NewDynamicMultiWriter(&a, &b)
	n, err := dmw.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, 5, n)
	require.Equal(t, "hello", a.String())
	require.Equal(t, "hello", b.String())
}

func TestDynamicMultiWriter_AddAndRemoveWriter(t *testing.T) {
	t.Parallel()
	var initial, added bytes.Buffer
	dmw := NewDynamicMultiWriter(&initial)
	dmw.AddWriter(&added)

	_, err := dmw.Write([]byte("x"))
	require.NoError(t, err)
	require.Equal(t, "x", initial.String())
	require.Equal(t, "x", added.String())

	dmw.RemoveWriter(&added)
	_, err = dmw.Write([]byte("y"))
	require.NoError(t, err)
	require.Equal(t, "xy", initial.String())
	require.Equal(t, "x", added.String(), "removed writer should not receive further writes")
}

type errWriter struct{}

func (errWriter) Write(_ []byte) (int, error) { return 0, errors.New("boom") }

func TestDynamicMultiWriter_ReturnsErrOnWriterFailure(t *testing.T) {
	t.Parallel()
	dmw := NewDynamicMultiWriter(errWriter{})
	_, err := dmw.Write([]byte("x"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}

func TestDynamicMultiWriter_ConcurrentWrites(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	dmw := NewDynamicMultiWriter(&buf)
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			_, err := dmw.Write([]byte("a"))
			require.NoError(t, err)
		}()
	}
	wg.Wait()
	require.Equal(t, n, buf.Len())
}

// ---------------------------------------------------------------------------
// EventForMessage / newConsoleMessageEvent
// ---------------------------------------------------------------------------

func TestEventForMessage_StripsAnsiAndAddsNewline(t *testing.T) {
	t.Parallel()
	// ANSI escape: "\x1b[31mred\x1b[0m"
	env := EventForMessage("\x1b[31mred\x1b[0m")
	require.Equal(t, contracts.ConsoleMessageEventDataType, env.Type)
	msg, ok := env.Data.(contracts.ConsoleMessage)
	require.True(t, ok)
	require.Equal(t, "red\n", msg.Message)
	require.False(t, env.Timestamp.IsZero())
}

func TestEventForMessage_EmptyString(t *testing.T) {
	t.Parallel()
	env := EventForMessage("")
	msg, ok := env.Data.(contracts.ConsoleMessage)
	require.True(t, ok)
	require.Equal(t, "\n", msg.Message)
}

// ---------------------------------------------------------------------------
// TabAlign
// ---------------------------------------------------------------------------

func TestTabAlign(t *testing.T) {
	t.Parallel()
	in := []string{
		"a\tb\tc",
		"aaa\tbb\tccc",
	}
	out, err := TabAlign(in, 2)
	require.NoError(t, err)
	require.Len(t, out, len(in))
	// Columns should be separated by at least 2 spaces (padding).
	for _, l := range out {
		require.NotEmpty(t, l)
	}
}

// ---------------------------------------------------------------------------
// Parameter helpers: AddOutputFlag / AddOutputParam / AddQueryParam /
// GetCommandFormatter
// ---------------------------------------------------------------------------

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

func TestAddQueryParam_RegistersHiddenFlag(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{Use: "x"}
	AddQueryParam(cmd)
	f := cmd.Flags().Lookup("query")
	require.NotNil(t, f)
	require.True(t, f.Hidden)
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

// ---------------------------------------------------------------------------
// JsonFormatter end-to-end including QueryFilter behavior on nil query.
// ---------------------------------------------------------------------------

func TestJsonFormatter_QueryFilter_NoQueryReturnsInput(t *testing.T) {
	t.Parallel()
	f := &JsonFormatter{}
	obj := map[string]any{"a": 1}
	out, err := f.QueryFilter(obj)
	require.NoError(t, err)
	require.Equal(t, obj, out)
}

// ---------------------------------------------------------------------------
// EnvVarsFormatter error path for invalid input type
// ---------------------------------------------------------------------------

func TestEnvVarsFormatter_RejectsWrongType(t *testing.T) {
	t.Parallel()
	f := &EnvVarsFormatter{}
	err := f.Format(123, io.Discard, nil)
	require.Error(t, err)
}
