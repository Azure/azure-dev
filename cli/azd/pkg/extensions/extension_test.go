// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package extensions

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExtension_Initialize_SignalsReadiness(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	ext.Initialize()

	err := ext.WaitUntilReady(t.Context())
	require.NoError(t, err)
}

func TestExtension_Initialize_Idempotent(t *testing.T) {
	t.Parallel()

	ext := &Extension{}

	// Calling Initialize twice must not panic or block.
	ext.Initialize()
	ext.Initialize()

	err := ext.WaitUntilReady(t.Context())
	require.NoError(t, err)
}

func TestExtension_Fail_SignalsError(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	expected := errors.New("extension startup failed")
	ext.Fail(expected)

	err := ext.WaitUntilReady(t.Context())
	require.ErrorIs(t, err, expected)
}

func TestExtension_WaitUntilReady_CancelledContext(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	err := ext.WaitUntilReady(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestExtension_WaitUntilReady_Timeout(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancel()

	err := ext.WaitUntilReady(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExtension_HasCapability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		capabilities []CapabilityType
		query        []CapabilityType
		want         bool
	}{
		{
			name:         "SinglePresent",
			capabilities: []CapabilityType{CustomCommandCapability, McpServerCapability},
			query:        []CapabilityType{CustomCommandCapability},
			want:         true,
		},
		{
			name:         "SingleMissing",
			capabilities: []CapabilityType{CustomCommandCapability},
			query:        []CapabilityType{McpServerCapability},
			want:         false,
		},
		{
			name:         "MultipleAllPresent",
			capabilities: []CapabilityType{CustomCommandCapability, McpServerCapability, MetadataCapability},
			query:        []CapabilityType{CustomCommandCapability, McpServerCapability},
			want:         true,
		},
		{
			name:         "MultipleOneMissing",
			capabilities: []CapabilityType{CustomCommandCapability},
			query:        []CapabilityType{CustomCommandCapability, McpServerCapability},
			want:         false,
		},
		{
			name:         "EmptyQuery",
			capabilities: []CapabilityType{CustomCommandCapability},
			query:        []CapabilityType{},
			want:         true,
		},
		{
			name:         "EmptyCapabilities",
			capabilities: []CapabilityType{},
			query:        []CapabilityType{CustomCommandCapability},
			want:         false,
		},
		{
			name:         "NilCapabilities",
			capabilities: nil,
			query:        []CapabilityType{CustomCommandCapability},
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ext := &Extension{Capabilities: tt.capabilities}
			got := ext.HasCapability(tt.query...)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestExtension_StdIn_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	reader := ext.StdIn()
	require.NotNil(t, reader)
}

func TestExtension_StdOut_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	writer := ext.StdOut()
	require.NotNil(t, writer)
}

func TestExtension_StdErr_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	writer := ext.StdErr()
	require.NotNil(t, writer)
}

func TestExtension_ReportedError_RoundTrip(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	expected := errors.New("something went wrong")

	ext.SetReportedError(expected)
	got := ext.GetReportedError()
	require.ErrorIs(t, got, expected)
}

func TestExtension_GetReportedError_NilByDefault(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	got := ext.GetReportedError()
	require.NoError(t, got)
}

func TestExtension_ReportedError_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	ext := &Extension{}
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	for range goroutines {
		err := errors.New("error from writer")

		go func() {
			defer wg.Done()
			ext.SetReportedError(err)
		}()

		go func() {
			defer wg.Done()
			_ = ext.GetReportedError()
		}()
	}

	wg.Wait()

	// After all goroutines complete, the reported error should be one of the
	// written errors (non-nil) — the exact value depends on scheduling.
	got := ext.GetReportedError()
	require.Error(t, got)
}
