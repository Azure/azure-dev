// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"azureaieval/internal/project"

	"github.com/stretchr/testify/require"
)

func warnFor(t *testing.T, traces *project.TraceSpec) string {
	t.Helper()
	cfg := &project.GenerateConfig{}
	cfg.Agent.Context.Traces = traces

	var buf bytes.Buffer
	warnIgnoredTraceFields(cfg, &buf)
	return buf.String()
}

// source and sample are accepted by the config model but the generation API
// takes only a day window, so they are dropped. Dropping them silently lets an
// author believe they narrowed the trace selection when nothing changed.
func TestWarnsAboutTraceFieldsWithNoEffect(t *testing.T) {
	out := warnFor(t, &project.TraceSpec{Source: "production", Window: "30d", Sample: 500})
	require.Contains(t, out, "agent.context.traces.source")
	require.Contains(t, out, "agent.context.traces.sample")
	require.Contains(t, out, "window")
	require.Contains(t, out, "have no effect", "two fields take a plural verb")

	out = warnFor(t, &project.TraceSpec{Source: "production", Window: "30d"})
	require.Contains(t, out, "agent.context.traces.source")
	require.NotContains(t, out, "sample")
	require.Contains(t, out, "has no effect", "one field takes a singular verb")
}

// The field that does work draws no warning, and neither does an absent block.
func TestNoWarningWhenOnlyWindowIsSet(t *testing.T) {
	require.Empty(t, warnFor(t, &project.TraceSpec{Window: "30d"}))
	require.Empty(t, warnFor(t, nil))
	require.Empty(t, warnFor(t, &project.TraceSpec{}))
}
