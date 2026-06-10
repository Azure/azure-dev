// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package validate

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// testGate is a simple Gate implementation for testing.
type testGate struct {
	name   string
	result *GateResult
	err    error
}

func (g *testGate) Name() string { return g.name }

func (g *testGate) Run(_ context.Context, _ *PipelineContext) (*GateResult, error) {
	return g.result, g.err
}

func TestPipeline_Run_NoGates(t *testing.T) {
	p := NewPipeline(PipelineOptions{})
	result, err := p.Run(t.Context(), &PipelineContext{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.GateResults)
	require.False(t, result.HasErrors())
	require.False(t, result.HasWarnings())
}

func TestPipeline_Run_SingleGateNoFindings(t *testing.T) {
	p := NewPipeline(PipelineOptions{})
	p.AddGate(&testGate{
		name: "empty-gate",
		result: &GateResult{
			GateName: "empty-gate",
			Results:  []CheckResult{},
		},
	})

	result, err := p.Run(t.Context(), &PipelineContext{})
	require.NoError(t, err)
	require.Len(t, result.GateResults, 1)
	require.Equal(t, "empty-gate", result.GateResults[0].GateName)
	require.Empty(t, result.GateResults[0].Results)
	require.False(t, result.HasErrors())
}

func TestPipeline_Run_WarningsDoNotAbort(t *testing.T) {
	p := NewPipeline(PipelineOptions{OnError: OnErrorAbort})
	p.AddGate(&testGate{
		name: "warning-gate",
		result: &GateResult{
			GateName: "warning-gate",
			Results: []CheckResult{
				{Severity: CheckWarning, DiagnosticID: "warn1", Message: "a warning"},
			},
		},
	})
	p.AddGate(&testGate{
		name: "second-gate",
		result: &GateResult{
			GateName: "second-gate",
			Results:  []CheckResult{},
		},
	})

	result, err := p.Run(t.Context(), &PipelineContext{})
	require.NoError(t, err)
	// Both gates should have run — warnings don't abort.
	require.Len(t, result.GateResults, 2)
	require.True(t, result.HasWarnings())
	require.False(t, result.HasErrors())
}

func TestPipeline_Run_ErrorsAbortByDefault(t *testing.T) {
	p := NewPipeline(PipelineOptions{})
	p.AddGate(&testGate{
		name: "error-gate",
		result: &GateResult{
			GateName: "error-gate",
			Results: []CheckResult{
				{Severity: CheckError, DiagnosticID: "err1", Message: "a blocking error"},
			},
		},
	})
	p.AddGate(&testGate{
		name: "should-not-run",
		result: &GateResult{
			GateName: "should-not-run",
			Results:  []CheckResult{},
		},
	})

	result, err := p.Run(t.Context(), &PipelineContext{})
	require.NoError(t, err)
	// Only the first gate should have run.
	require.Len(t, result.GateResults, 1)
	require.True(t, result.HasErrors())
	require.Equal(t, 1, result.TotalErrors())
}

func TestPipeline_Run_ErrorsContinueWhenConfigured(t *testing.T) {
	p := NewPipeline(PipelineOptions{OnError: OnErrorContinue})
	p.AddGate(&testGate{
		name: "error-gate",
		result: &GateResult{
			GateName: "error-gate",
			Results: []CheckResult{
				{Severity: CheckError, DiagnosticID: "err1", Message: "an error"},
			},
		},
	})
	p.AddGate(&testGate{
		name: "next-gate",
		result: &GateResult{
			GateName: "next-gate",
			Results: []CheckResult{
				{Severity: CheckWarning, DiagnosticID: "warn1", Message: "a warning"},
			},
		},
	})

	result, err := p.Run(t.Context(), &PipelineContext{})
	require.NoError(t, err)
	// Both gates should have run.
	require.Len(t, result.GateResults, 2)
	require.True(t, result.HasErrors())
	require.True(t, result.HasWarnings())
	require.Equal(t, 1, result.TotalErrors())
	require.Equal(t, 1, result.TotalWarnings())
}

func TestPipeline_Run_GateErrorStopsPipeline(t *testing.T) {
	p := NewPipeline(PipelineOptions{})
	p.AddGate(&testGate{
		name: "failing-gate",
		err:  errors.New("internal failure"),
	})
	p.AddGate(&testGate{
		name:   "should-not-run",
		result: &GateResult{GateName: "should-not-run"},
	})

	result, err := p.Run(t.Context(), &PipelineContext{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failing-gate")
	require.Empty(t, result.GateResults)
}

func TestPipeline_Run_NilResultTreatedAsSkipped(t *testing.T) {
	p := NewPipeline(PipelineOptions{})
	p.AddGate(&testGate{
		name:   "nil-gate",
		result: nil,
	})

	result, err := p.Run(t.Context(), &PipelineContext{})
	require.NoError(t, err)
	require.Len(t, result.GateResults, 1)
	require.True(t, result.GateResults[0].Skipped)
}

func TestPipeline_Run_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel immediately

	p := NewPipeline(PipelineOptions{})
	p.AddGate(&testGate{
		name:   "should-not-run",
		result: &GateResult{GateName: "should-not-run"},
	})

	result, err := p.Run(ctx, &PipelineContext{})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, result.GateResults)
}

func TestPipeline_Run_GateOrder(t *testing.T) {
	p := NewPipeline(PipelineOptions{})
	names := []string{"alpha", "beta", "gamma"}
	for _, name := range names {
		p.AddGate(&testGate{
			name:   name,
			result: &GateResult{GateName: name, Results: []CheckResult{}},
		})
	}

	result, err := p.Run(t.Context(), &PipelineContext{})
	require.NoError(t, err)
	require.Len(t, result.GateResults, 3)
	for i, name := range names {
		require.Equal(t, name, result.GateResults[i].GateName)
	}
}

func TestPipelineContext_Values(t *testing.T) {
	pCtx := &PipelineContext{}

	// GetValue on empty context
	_, ok := GetValue[string](pCtx, "missing")
	require.False(t, ok)

	// SetValue and GetValue
	pCtx.SetValue("gate-a.data", "hello")
	val, ok := GetValue[string](pCtx, "gate-a.data")
	require.True(t, ok)
	require.Equal(t, "hello", val)

	// Type mismatch
	_, ok = GetValue[int](pCtx, "gate-a.data")
	require.False(t, ok)
}

func TestPipelineResult_Totals(t *testing.T) {
	r := &PipelineResult{
		GateResults: []*GateResult{
			{
				GateName: "g1",
				Results: []CheckResult{
					{Severity: CheckWarning},
					{Severity: CheckError},
					{Severity: CheckWarning},
				},
			},
			{
				GateName: "g2",
				Results: []CheckResult{
					{Severity: CheckError},
				},
			},
		},
	}

	require.Equal(t, 2, r.TotalErrors())
	require.Equal(t, 2, r.TotalWarnings())
	require.True(t, r.HasErrors())
	require.True(t, r.HasWarnings())
}
