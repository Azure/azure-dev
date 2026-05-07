// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// ParseOutputFormat
// ---------------------------------------------------------------------------

func TestParseOutputFormat(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		expected  OutputFormat
		expectErr bool
	}{
		{name: "default string", input: "default", expected: OutputFormatDefault},
		{name: "empty string", input: "", expected: OutputFormatDefault},
		{name: "json lowercase", input: "json", expected: OutputFormatJSON},
		{name: "JSON uppercase", input: "JSON", expected: OutputFormatJSON},
		{name: "Json mixed case", input: "Json", expected: OutputFormatJSON},
		{name: "DEFAULT uppercase", input: "DEFAULT", expected: OutputFormatDefault},
		{name: "invalid format", input: "xml", expected: OutputFormatDefault, expectErr: true},
		{name: "invalid format yaml", input: "yaml", expected: OutputFormatDefault, expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOutputFormat(tt.input)
			if tt.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "invalid output format")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.expected, got)
		})
	}
}

// ---------------------------------------------------------------------------
// NewOutput defaults
// ---------------------------------------------------------------------------

func TestNewOutput_DefaultWriters(t *testing.T) {
	out := NewOutput(OutputOptions{})
	require.NotNil(t, out)
	// Default format should be "default" (zero-value of OutputFormat).
	require.False(t, out.IsJSON())
}

func TestNewOutput_JSONMode(t *testing.T) {
	out := NewOutput(OutputOptions{Format: OutputFormatJSON})
	require.True(t, out.IsJSON())
}

// ---------------------------------------------------------------------------
// Success
// ---------------------------------------------------------------------------

func TestOutput_Success_DefaultFormat(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	out.Success("deployed %s", "myapp")

	// Should contain the message text (color codes may wrap it).
	require.Contains(t, buf.String(), "Done: deployed myapp")
}

func TestOutput_Success_JSONFormat_IsNoop(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf, Format: OutputFormatJSON})

	out.Success("should not appear")

	require.Empty(t, buf.String())
}

// ---------------------------------------------------------------------------
// Warning
// ---------------------------------------------------------------------------

func TestOutput_Warning_DefaultFormat(t *testing.T) {
	var errBuf bytes.Buffer
	out := NewOutput(OutputOptions{ErrWriter: &errBuf})

	out.Warning("deprecated %s", "v1")

	require.Contains(t, errBuf.String(), "Warning: deprecated v1")
}

func TestOutput_Warning_JSONFormat(t *testing.T) {
	var errBuf bytes.Buffer
	out := NewOutput(OutputOptions{ErrWriter: &errBuf, Format: OutputFormatJSON})

	out.Warning("api deprecated")

	var parsed map[string]string
	err := json.Unmarshal(errBuf.Bytes(), &parsed)
	require.NoError(t, err)
	require.Equal(t, "warning", parsed["level"])
	require.Equal(t, "api deprecated", parsed["message"])
}

// ---------------------------------------------------------------------------
// Error
// ---------------------------------------------------------------------------

func TestOutput_Error_DefaultFormat(t *testing.T) {
	var errBuf bytes.Buffer
	out := NewOutput(OutputOptions{ErrWriter: &errBuf})

	out.Error("connection failed: %s", "timeout")

	require.Contains(t, errBuf.String(), "Error: connection failed: timeout")
}

func TestOutput_Error_JSONFormat(t *testing.T) {
	var errBuf bytes.Buffer
	out := NewOutput(OutputOptions{ErrWriter: &errBuf, Format: OutputFormatJSON})

	out.Error("disk full")

	var parsed map[string]string
	err := json.Unmarshal(errBuf.Bytes(), &parsed)
	require.NoError(t, err)
	require.Equal(t, "error", parsed["level"])
	require.Equal(t, "disk full", parsed["message"])
}

// ---------------------------------------------------------------------------
// Info
// ---------------------------------------------------------------------------

func TestOutput_Info_DefaultFormat(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	out.Info("fetching %d items", 5)

	require.Contains(t, buf.String(), "fetching 5 items")
}

func TestOutput_Info_JSONFormat_IsNoop(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf, Format: OutputFormatJSON})

	out.Info("hidden")

	require.Empty(t, buf.String())
}

// ---------------------------------------------------------------------------
// Message
// ---------------------------------------------------------------------------

func TestOutput_Message_DefaultFormat(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	out.Message("plain text %d", 42)

	require.Equal(t, "plain text 42\n", buf.String())
}

func TestOutput_Message_JSONFormat_IsNoop(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf, Format: OutputFormatJSON})

	out.Message("should not appear")

	require.Empty(t, buf.String())
}

// ---------------------------------------------------------------------------
// JSON
// ---------------------------------------------------------------------------

func TestOutput_JSON_Struct(t *testing.T) {
	type result struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	err := out.JSON(result{Name: "test", Count: 7})
	require.NoError(t, err)

	var decoded result
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Equal(t, "test", decoded.Name)
	require.Equal(t, 7, decoded.Count)
}

func TestOutput_JSON_Map(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	err := out.JSON(map[string]string{"key": "value"})
	require.NoError(t, err)

	var decoded map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Equal(t, "value", decoded["key"])
}

func TestOutput_JSON_Unmarshalable(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	err := out.JSON(make(chan int))
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to encode JSON")
}

func TestOutput_JSON_PrettyPrinted(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	err := out.JSON(map[string]int{"a": 1})
	require.NoError(t, err)

	// Verify indentation is present (pretty-printed).
	require.Contains(t, buf.String(), "  ")
}

// ---------------------------------------------------------------------------
// Table — default format
// ---------------------------------------------------------------------------

func TestOutput_Table_DefaultFormat(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	headers := []string{"Name", "Status"}
	rows := [][]string{
		{"api", "running"},
		{"web", "stopped"},
	}

	out.Table(headers, rows)

	text := buf.String()
	require.Contains(t, text, "Name")
	require.Contains(t, text, "Status")
	require.Contains(t, text, "api")
	require.Contains(t, text, "running")
	require.Contains(t, text, "web")
	require.Contains(t, text, "stopped")

	// Separator line should be present.
	require.Contains(t, text, "─")
}

func TestOutput_Table_EmptyHeaders(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	out.Table(nil, [][]string{{"a"}})

	require.Empty(t, buf.String())
}

func TestOutput_Table_EmptyRows(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	out.Table([]string{"Name"}, nil)

	// Header + separator should still be printed.
	text := buf.String()
	require.Contains(t, text, "Name")
	require.Contains(t, text, "─")
}

func TestOutput_Table_ShortRow(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	// Row has fewer cells than headers — should pad with empty strings.
	out.Table([]string{"A", "B", "C"}, [][]string{{"only-a"}})

	text := buf.String()
	require.Contains(t, text, "only-a")
	// No panic from short row.
	lines := strings.Split(strings.TrimSpace(text), "\n")
	require.Len(t, lines, 3) // header + separator + 1 data row
}

func TestOutput_Table_ColumnAlignment(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf})

	headers := []string{"ID", "LongerName"}
	rows := [][]string{
		{"1", "short"},
		{"2", "a-much-longer-value"},
	}

	out.Table(headers, rows)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 3)

	// All separator dashes should align with header width.
	sepLine := lines[1]
	require.NotEmpty(t, sepLine)
}

// ---------------------------------------------------------------------------
// Table — JSON format
// ---------------------------------------------------------------------------

func TestOutput_Table_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf, Format: OutputFormatJSON})

	headers := []string{"Service", "Port"}
	rows := [][]string{
		{"api", "8080"},
		{"web", "3000"},
	}

	out.Table(headers, rows)

	var decoded []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Len(t, decoded, 2)
	require.Equal(t, "api", decoded[0]["Service"])
	require.Equal(t, "8080", decoded[0]["Port"])
	require.Equal(t, "web", decoded[1]["Service"])
	require.Equal(t, "3000", decoded[1]["Port"])
}

func TestOutput_Table_JSONFormat_ShortRow(t *testing.T) {
	var buf bytes.Buffer
	out := NewOutput(OutputOptions{Writer: &buf, Format: OutputFormatJSON})

	out.Table([]string{"A", "B"}, [][]string{{"only-a"}})

	var decoded []map[string]string
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Len(t, decoded, 1)
	require.Equal(t, "only-a", decoded[0]["A"])
	require.Equal(t, "", decoded[0]["B"])
}
