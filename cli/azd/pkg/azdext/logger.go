// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// LoggerOptions configures [SetupLogging] and [NewLogger].
type LoggerOptions struct {
	// Debug enables debug-level logging. When false, messages below Info are
	// suppressed. If not set explicitly, [NewLogger] checks the AZD_DEBUG
	// environment variable.
	Debug bool
	// Structured selects JSON output when true, human-readable text when false.
	Structured bool
	// Writer overrides the output destination. Defaults to os.Stderr.
	Writer io.Writer
}

// SetupLogging configures the process-wide default [slog.Logger].
// It is typically called once at startup (for example from
// [NewExtensionRootCommand]'s PersistentPreRunE callback).
//
// Calling SetupLogging is optional — [NewLogger] works without it and creates
// loggers that inherit from [slog.Default]. SetupLogging is provided for
// extensions that want explicit control over the global log level and format.
func SetupLogging(opts LoggerOptions) {
	handler := newHandler(opts)
	slog.SetDefault(slog.New(handler))
}

// Logger provides component-scoped structured logging built on [log/slog].
//
// Each Logger carries a "component" attribute so log lines can be filtered or
// routed by subsystem. Additional context can be attached via [Logger.With],
// [Logger.WithComponent], or [Logger.WithOperation].
//
// Logger writes to stderr by default and never writes to stdout, so it does
// not interfere with command output or JSON-mode piping.
type Logger struct {
	slogger   *slog.Logger
	component string
}

// NewLogger creates a Logger scoped to the given component name.
//
// If the AZD_DEBUG environment variable is set to a truthy value ("1", "true",
// "yes") and opts.Debug is false, debug logging is enabled automatically. This
// lets extension authors respect the framework's debug flag without extra
// plumbing.
//
// When opts is omitted (zero value), the logger uses Info level with text
// format on stderr.
func NewLogger(component string, opts ...LoggerOptions) *Logger {
	var o LoggerOptions
	if len(opts) > 0 {
		o = opts[0]
	}

	// Auto-detect debug from environment when not explicitly set.
	if !o.Debug {
		o.Debug = isDebugEnv()
	}

	handler := newHandler(o)
	base := slog.New(handler).With("component", component)

	return &Logger{
		slogger:   base,
		component: component,
	}
}

// Component returns the component name this logger was created with.
func (l *Logger) Component() string {
	return l.component
}

// Debug logs a message at debug level with optional key-value pairs.
func (l *Logger) Debug(msg string, args ...any) {
	l.slogger.Debug(msg, args...)
}

// Info logs a message at info level with optional key-value pairs.
func (l *Logger) Info(msg string, args ...any) {
	l.slogger.Info(msg, args...)
}

// Warn logs a message at warn level with optional key-value pairs.
func (l *Logger) Warn(msg string, args ...any) {
	l.slogger.Warn(msg, args...)
}

// Error logs a message at error level with optional key-value pairs.
func (l *Logger) Error(msg string, args ...any) {
	l.slogger.Error(msg, args...)
}

// With returns a new Logger that includes the given key-value pairs in every
// subsequent log entry. Keys must be strings; values can be any type
// supported by [slog].
//
// Example:
//
//	l := logger.With("request_id", reqID)
//	l.Info("processing")   // includes component + request_id
func (l *Logger) With(args ...any) *Logger {
	return &Logger{
		slogger:   l.slogger.With(args...),
		component: l.component,
	}
}

// WithComponent returns a new Logger with a different component name. The
// original component is preserved as "parent_component".
func (l *Logger) WithComponent(name string) *Logger {
	return &Logger{
		slogger:   l.slogger.With("parent_component", l.component, "component", name),
		component: name,
	}
}

// WithOperation returns a new Logger with an "operation" attribute.
func (l *Logger) WithOperation(name string) *Logger {
	return &Logger{
		slogger:   l.slogger.With("operation", name),
		component: l.component,
	}
}

// Slogger returns the underlying [*slog.Logger] for advanced use cases such
// as passing to libraries that accept a standard slog logger.
func (l *Logger) Slogger() *slog.Logger {
	return l.slogger
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// newHandler creates an slog.Handler from LoggerOptions.
func newHandler(opts LoggerOptions) slog.Handler {
	w := opts.Writer
	if w == nil {
		w = os.Stderr
	}

	level := slog.LevelInfo
	if opts.Debug {
		level = slog.LevelDebug
	}

	handlerOpts := &slog.HandlerOptions{Level: level}

	if opts.Structured {
		return slog.NewJSONHandler(w, handlerOpts)
	}
	return slog.NewTextHandler(w, handlerOpts)
}

// isDebugEnv checks the AZD_DEBUG environment variable.
func isDebugEnv() bool {
	v := os.Getenv("AZD_DEBUG")
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		// Also accept "yes" for backward compatibility.
		return strings.EqualFold(v, "yes")
	}
	return b
}
