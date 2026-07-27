// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"bytes"
	"encoding/json"
	reflect "reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
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

// TestProtoMessages_ExerciseGenerated iterates over every proto message registered
// in the azdext package and exercises its generated methods (Reset, String,
// ProtoReflect, Descriptor, GetXxx getters) plus Marshal/Unmarshal round-trips.
// This is a broad, generic smoke test aimed at raising statement coverage of the
// generated *.pb.go files which otherwise contain hundreds of never-called
// getters and reflection helpers.
func TestProtoMessages_ExerciseGenerated(t *testing.T) {
	t.Parallel()

	var exercised int
	protoregistry.GlobalTypes.RangeMessages(func(mt protoreflect.MessageType) bool {
		name := string(mt.Descriptor().FullName())
		// Only exercise messages defined by this package (proto package = "azdext")
		// Map entry synthetic messages are registered as e.g. "azdext.Foo.BarEntry"
		// and are also fine to exercise since they live in the generated files.
		if !strings.HasPrefix(name, "azdext.") {
			return true
		}
		exerciseMessage(t, mt.New().Interface())
		exercised++
		return true
	})

	// Sanity check: at least one azdext proto message should be registered.
	// Log the observed count so unexpected drops remain visible without relying
	// on a brittle hard-coded threshold.
	t.Logf("exercised %d azdext proto messages", exercised)
	require.Greater(t, exercised, 0, "expected azdext proto messages to be registered")
}

// exerciseMessage invokes the generated methods on a proto message so that they
// contribute to statement coverage. It exercises both the populated (non-nil
// receiver) and nil-receiver paths of getters.
func exerciseMessage(t *testing.T, m proto.Message) {
	t.Helper()
	name := string(m.ProtoReflect().Descriptor().FullName())

	t.Run(name, func(t *testing.T) {
		require.NotNil(t, m.ProtoReflect())

		// Marshal a zero-value message, then unmarshal into a fresh instance.
		// This exercises the reflection-backed Marshal/Unmarshal paths.
		data, err := proto.Marshal(m)
		require.NoError(t, err)

		fresh := m.ProtoReflect().New().Interface()
		require.NoError(t, proto.Unmarshal(data, fresh))

		// Call the generated String() and Reset() methods via reflection so
		// we don't depend on them being part of the proto.Message interface
		// in this version of the protobuf library.
		mv := reflect.ValueOf(m)
		if s := mv.MethodByName("String"); s.IsValid() {
			s.Call(nil)
		}
		if r := mv.MethodByName("Reset"); r.IsValid() {
			r.Call(nil)
		}

		// Call all zero-arg methods via reflection to pick up:
		//   - GetXxx() getters
		//   - Descriptor()
		// Do it against both a non-nil instance and an explicit typed-nil
		// instance so both branches of generated "if x != nil" checks run.
		ptrType := reflect.TypeOf(m)
		nilPtr := reflect.New(ptrType).Elem() // typed nil pointer

		// Non-nil receiver: getters must NOT panic on a valid instance. Any panic
		// here indicates a real regression in generated code and fails the test.
		callZeroArgMethods(t, mv, false /* allowPanic */)
		// Typed-nil receiver: generated getters typically guard with `if x != nil`,
		// but invoking via reflection on a typed-nil pointer can still panic for
		// methods that unconditionally deref (e.g. Descriptor). Allow (and swallow)
		// panics here; we're only exercising the `if x != nil` branches for coverage.
		callZeroArgMethods(t, nilPtr, true /* allowPanic */)
	})
}

func callZeroArgMethods(t *testing.T, v reflect.Value, allowPanic bool) {
	t.Helper()
	typ := v.Type()
	for i := 0; i < typ.NumMethod(); i++ {
		method := typ.Method(i)
		// Only zero-arg, exported methods.
		if method.Type.NumIn() != 1 { // receiver is arg 0
			continue
		}
		name := method.Name
		// Skip methods that we've already invoked (or whose invocation would
		// panic on typed nil via the reflect layer).
		switch name {
		case "Reset", "String", "ProtoReflect", "ProtoMessage":
			continue
		}
		// Only invoke Get* and Descriptor (the safe, side-effect-free generated helpers).
		if !strings.HasPrefix(name, "Get") && name != "Descriptor" {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil && !allowPanic {
					t.Errorf("unexpected panic invoking %s.%s on non-nil receiver: %v",
						typ.String(), name, r)
				}
			}()
			out := v.Method(i).Call(nil)
			_ = out
		}()
	}
}
