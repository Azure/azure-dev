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
// NewLogger — basic construction
// ---------------------------------------------------------------------------

func TestNewLogger_DefaultOptions(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("test-component", LoggerOptions{Writer: &buf})

	require.NotNil(t, logger)
	require.Equal(t, "test-component", logger.Component())
}

func TestNewLogger_ZeroOptions(t *testing.T) {
	// Zero-value opts should not panic (writes to stderr).
	logger := NewLogger("safe")
	require.NotNil(t, logger)
	require.Equal(t, "safe", logger.Component())
}

func TestNewLogger_NoOpts(t *testing.T) {
	// Calling without variadic opts should not panic.
	logger := NewLogger("minimal")
	require.NotNil(t, logger)
}

// ---------------------------------------------------------------------------
// Log levels — Info
// ---------------------------------------------------------------------------

func TestLogger_Info(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("mycomp", LoggerOptions{Writer: &buf})

	logger.Info("hello world", "key", "val")

	output := buf.String()
	require.Contains(t, output, "hello world")
	require.Contains(t, output, "key=val")
	require.Contains(t, output, "component=mycomp")
}

// ---------------------------------------------------------------------------
// Log levels — Debug
// ---------------------------------------------------------------------------

func TestLogger_Debug_Enabled(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("dbg", LoggerOptions{Debug: true, Writer: &buf})

	logger.Debug("debug message", "detail", "x")

	require.Contains(t, buf.String(), "debug message")
	require.Contains(t, buf.String(), "detail=x")
}

func TestLogger_Debug_Disabled(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("dbg", LoggerOptions{Debug: false, Writer: &buf})

	logger.Debug("should not appear")

	require.Empty(t, buf.String())
}

func TestLogger_Debug_AZD_DEBUG_EnvVar(t *testing.T) {
	t.Setenv("AZD_DEBUG", "true")

	var buf bytes.Buffer
	logger := NewLogger("env-debug", LoggerOptions{Writer: &buf})

	logger.Debug("from env var")

	require.Contains(t, buf.String(), "from env var")
}

func TestLogger_Debug_AZD_DEBUG_EnvVar_One(t *testing.T) {
	t.Setenv("AZD_DEBUG", "1")

	var buf bytes.Buffer
	logger := NewLogger("env-one", LoggerOptions{Writer: &buf})

	logger.Debug("debug via 1")

	require.Contains(t, buf.String(), "debug via 1")
}

func TestLogger_Debug_AZD_DEBUG_EnvVar_Yes(t *testing.T) {
	t.Setenv("AZD_DEBUG", "yes")

	var buf bytes.Buffer
	logger := NewLogger("env-yes", LoggerOptions{Writer: &buf})

	logger.Debug("debug via yes")

	require.Contains(t, buf.String(), "debug via yes")
}

func TestLogger_Debug_AZD_DEBUG_Unset(t *testing.T) {
	t.Setenv("AZD_DEBUG", "")

	var buf bytes.Buffer
	logger := NewLogger("env-empty", LoggerOptions{Writer: &buf})

	logger.Debug("hidden")

	require.Empty(t, buf.String())
}

// ---------------------------------------------------------------------------
// Log levels — Warn / Error
// ---------------------------------------------------------------------------

func TestLogger_Warn(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("warn-test", LoggerOptions{Writer: &buf})

	logger.Warn("something concerning", "retries", 3)

	require.Contains(t, buf.String(), "something concerning")
	require.Contains(t, buf.String(), "retries=3")
}

func TestLogger_Error(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("err-test", LoggerOptions{Writer: &buf})

	logger.Error("bad thing happened", "code", 500)

	require.Contains(t, buf.String(), "bad thing happened")
	require.Contains(t, buf.String(), "code=500")
}

// ---------------------------------------------------------------------------
// Structured (JSON) output
// ---------------------------------------------------------------------------

func TestLogger_StructuredJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("json-comp", LoggerOptions{Structured: true, Writer: &buf})

	logger.Info("structured entry", "env", "prod")

	// Each line should be valid JSON.
	var parsed map[string]any
	err := json.Unmarshal(buf.Bytes(), &parsed)
	require.NoError(t, err)
	require.Equal(t, "structured entry", parsed["msg"])
	require.Equal(t, "prod", parsed["env"])
	require.Equal(t, "json-comp", parsed["component"])
}

// ---------------------------------------------------------------------------
// Context chaining — With
// ---------------------------------------------------------------------------

func TestLogger_With(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("base", LoggerOptions{Writer: &buf})

	child := logger.With("request_id", "abc-123")
	child.Info("processing")

	output := buf.String()
	require.Contains(t, output, "request_id=abc-123")
	require.Contains(t, output, "component=base")
	require.Contains(t, output, "processing")

	// Child should have the same component.
	require.Equal(t, "base", child.Component())
}

func TestLogger_With_ChainMultiple(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("chain", LoggerOptions{Writer: &buf})

	child := logger.With("a", "1").With("b", "2")
	child.Info("chained")

	output := buf.String()
	require.Contains(t, output, "a=1")
	require.Contains(t, output, "b=2")
}

// ---------------------------------------------------------------------------
// Context chaining — WithComponent
// ---------------------------------------------------------------------------

func TestLogger_WithComponent(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("parent", LoggerOptions{Structured: true, Writer: &buf})

	child := logger.WithComponent("child-subsystem")
	child.Info("from child")

	require.Equal(t, "child-subsystem", child.Component())

	var parsed map[string]any
	err := json.Unmarshal(buf.Bytes(), &parsed)
	require.NoError(t, err)
	require.Equal(t, "child-subsystem", parsed["component"])
	require.Equal(t, "parent", parsed["parent_component"])
}

// ---------------------------------------------------------------------------
// Context chaining — WithOperation
// ---------------------------------------------------------------------------

func TestLogger_WithOperation(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("ops", LoggerOptions{Writer: &buf})

	child := logger.WithOperation("deploy")
	child.Info("starting deploy")

	output := buf.String()
	require.Contains(t, output, "operation=deploy")
	require.Equal(t, "ops", child.Component())
}

// ---------------------------------------------------------------------------
// Slogger accessor
// ---------------------------------------------------------------------------

func TestLogger_Slogger(t *testing.T) {
	logger := NewLogger("access", LoggerOptions{Writer: &bytes.Buffer{}})
	require.NotNil(t, logger.Slogger())
}

// ---------------------------------------------------------------------------
// SetupLogging — global logger configuration
// ---------------------------------------------------------------------------

func TestSetupLogging_DoesNotPanic(t *testing.T) {
	// SetupLogging modifies slog.Default which is global state.
	// We only verify it does not panic here.
	var buf bytes.Buffer
	SetupLogging(LoggerOptions{Debug: true, Structured: true, Writer: &buf})

	// Restore a sensible default after the test.
	SetupLogging(LoggerOptions{Writer: &bytes.Buffer{}})
}

// ---------------------------------------------------------------------------
// isDebugEnv internal helper
// ---------------------------------------------------------------------------

func TestIsDebugEnv_Truthy(t *testing.T) {
	truthy := []string{"1", "true", "TRUE", "True", "yes", "YES", "Yes"}
	for _, v := range truthy {
		t.Run(v, func(t *testing.T) {
			t.Setenv("AZD_DEBUG", v)
			require.True(t, isDebugEnv())
		})
	}
}

func TestIsDebugEnv_Falsy(t *testing.T) {
	falsy := []string{"", "0", "false", "no", "maybe"}
	for _, v := range falsy {
		t.Run("value="+v, func(t *testing.T) {
			t.Setenv("AZD_DEBUG", v)
			require.False(t, isDebugEnv())
		})
	}
}

// ---------------------------------------------------------------------------
// Text format verification
// ---------------------------------------------------------------------------

func TestLogger_TextFormat_ContainsLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("lvl", LoggerOptions{Writer: &buf})

	logger.Info("test level")

	output := buf.String()
	require.True(t,
		strings.Contains(output, "INFO") || strings.Contains(output, "level=INFO"),
		"expected level indicator in text output: %s", output)
}
