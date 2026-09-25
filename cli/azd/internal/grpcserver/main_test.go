// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"fmt"
	"os"
	"testing"

	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The OTel global provider only ever delegates to the first provider
// installed in a process, and internal/tracing resolves its tracer at
// package init. A provider installed inside a single test would therefore
// be silently ignored, so the package installs exactly one here and tests
// share the recorder.
var (
	testSpanRecorder   = tracetest.NewSpanRecorder()
	testTracerProvider = tracesdk.NewTracerProvider(
		tracesdk.WithSpanProcessor(testSpanRecorder))
)

func TestMain(m *testing.M) {
	configDir, err := os.MkdirTemp("", "azd-grpcserver-tests-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("AZD_CONFIG_DIR", configDir); err != nil {
		panic(err)
	}
	if err := os.Setenv("AZURE_DEV_COLLECT_TELEMETRY", "no"); err != nil {
		panic(err)
	}

	otel.SetTracerProvider(testTracerProvider)
	code := m.Run()
	if err := os.RemoveAll(configDir); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove temporary azd config directory %q: %v\n", configDir, err)
	}
	os.Exit(code)
}
